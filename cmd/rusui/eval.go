package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/sannrox/rusui/internal/evalcorpus"
)

// Exit codes beyond usage (2): stale prior evidence, and a -gate that did
// not pass.
const (
	evalExitStale          = 3
	evalExitGateFailed     = 4
	evalExitGateIncomplete = 5
)

func evalCLI(args []string) {
	os.Exit(evalMain(args, os.Stdout, os.Stderr))
}

// evalMain replays a development split and an optional operator-owned
// held-out split, prints the report, and optionally preserves it, checks
// prior evidence against it, or requires one gate to pass.
func evalMain(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fs.SetOutput(errw)
	dev := fs.String("corpus", "eval/corpus/development.json", "development corpus file")
	heldOut := fs.String("heldout", "", "held-out corpus file (operator-owned; reported in aggregate only)")
	revision := fs.String("revision", "", "revision the evidence is for (default: build revision)")
	asJSON := fs.Bool("json", false, "print the JSON report instead of Markdown")
	outDir := fs.String("out", "", "also write the JSON report into this directory; never overwrites")
	prior := fs.String("check", "", "prior JSON report; exit 3 if it came from different inputs")
	gate := fs.String("gate", "", "exit 4 if this class's gate fails, 5 if it is incomplete")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(errw, "usage: rusui eval [-corpus FILE] [-heldout FILE] [-revision REV] [-json] [-out DIR] [-check PRIOR] [-gate CLASS]")
		return 2
	}
	rev := evalRevision(*revision)
	corpora := []*evalcorpus.Corpus{}
	for _, in := range []struct{ path, split string }{
		{*dev, evalcorpus.SplitDevelopment}, {*heldOut, evalcorpus.SplitHeldOut},
	} {
		if in.path == "" {
			continue
		}
		c, err := evalcorpus.Load(in.path, in.split)
		if err != nil {
			_, _ = fmt.Fprintln(errw, "eval:", err)
			return 1
		}
		corpora = append(corpora, c)
	}
	report, err := evalcorpus.Run(rev, corpora...)
	if err != nil {
		_, _ = fmt.Fprintln(errw, "eval:", err)
		return 1
	}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintln(errw, "eval:", err)
		return 1
	}
	body = append(body, '\n')
	if *asJSON {
		_, _ = out.Write(body)
	} else {
		_, _ = io.WriteString(out, evalcorpus.Markdown(report))
	}
	if *outDir != "" {
		path, err := preserveReport(*outDir, report, body)
		if err != nil {
			_, _ = fmt.Fprintln(errw, "eval:", err)
			return 1
		}
		_, _ = fmt.Fprintln(errw, "eval: wrote", path)
	}
	if *prior != "" {
		old, err := readReport(*prior)
		if err != nil {
			_, _ = fmt.Fprintln(errw, "eval:", err)
			return 1
		}
		if err := evalcorpus.SameEvidence(old, report); err != nil {
			_, _ = fmt.Fprintln(errw, "eval: stale evidence:", err)
			return evalExitStale
		}
	}
	if *gate != "" {
		return gateExit(report, *gate, errw)
	}
	return 0
}

func gateExit(r evalcorpus.Report, class string, errw io.Writer) int {
	for _, g := range r.Gates {
		if g.Class != class {
			continue
		}
		_, _ = fmt.Fprintf(errw, "eval: %s gate %s\n", class, g.Status)
		switch g.Status {
		case evalcorpus.GatePass:
			return 0
		case evalcorpus.GateFail:
			return evalExitGateFailed
		default:
			return evalExitGateIncomplete
		}
	}
	_, _ = fmt.Fprintf(errw, "eval: unknown gate %q\n", class)
	return 2
}

// preserveReport writes the report under a name derived from its inputs.
// An existing file is kept: prior results are never overwritten.
func preserveReport(dir string, r evalcorpus.Report, body []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// A readable revision plus a hash of the raw one, so distinct
	// revisions never share a report file.
	sum := sha256.Sum256([]byte(r.Revision))
	var name strings.Builder
	name.WriteString(strings.Map(func(r rune) rune {
		if r == '-' || r == '.' || r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') {
			return r
		}
		return '_'
	}, r.Revision) + "-" + hex.EncodeToString(sum[:4]))
	for _, s := range r.Splits {
		name.WriteString("-" + s.Split + "-" + s.Digest[:12])
	}
	path := filepath.Join(dir, name.String()+".json")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("%s exists; prior results are kept", path)
	}
	if err != nil {
		return "", err
	}
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return "", err
	}
	return path, f.Close()
}

func readReport(path string) (evalcorpus.Report, error) {
	var r evalcorpus.Report
	b, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("prior report %s: %w", path, err)
	}
	return r, nil
}

// evalRevision names the code a report is evidence for, from this
// binary's build information.
func evalRevision(override string) string {
	var stamp, modified string
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				stamp = s.Value
			case "vcs.modified":
				modified = s.Value
			}
		}
	}
	return revisionFrom(override, GitCommit, Version, stamp, modified)
}

// revisionFrom applies the trust rule. An explicit override is the
// operator's assertion and is trusted as given, except that a detected
// local change adds `-dirty`. Otherwise the build revision is trusted
// only when the linked commit (make builds) and a clean VCS stamp agree.
// Anything else gets `-unverified`: `go run`, -buildvcs=false, a plain
// `go build`, or a stamp Go took from an enclosing checkout. Both
// suffixes never match prior evidence (evalcorpus.SameEvidence).
func revisionFrom(override, commit, version, stamp, modified string) string {
	dirty := modified == "true" || strings.HasSuffix(version, "-dirty")
	if override != "" {
		if dirty {
			return override + "-dirty"
		}
		return override
	}
	rev := stamp
	linked := commit != "unknown" && commit != ""
	if linked {
		rev = commit
	}
	switch {
	case rev == "":
		return "unknown"
	case dirty:
		return rev + "-dirty"
	case !linked || modified != "false" || stamp != commit:
		return rev + "-unverified"
	}
	return rev
}

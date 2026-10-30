package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/sannrox/rusui/internal/compat"
)

func compatConformanceCLI(args []string) {
	os.Exit(compatConformanceMain(args, os.Stdout, os.Stderr))
}

// compatConformanceMain runs the 1.0 compatibility checks and prints a
// JSON report bound to this binary's commit and schema (issue #142).
func compatConformanceMain(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("compat-conformance", flag.ContinueOnError)
	fs.SetOutput(errOut)
	dir := fs.String("dir", "", "working directory for throwaway databases (default: temp)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	work := *dir
	if work == "" {
		tmp, err := os.MkdirTemp("", "rusui-compat-")
		if err != nil {
			_, _ = fmt.Fprintln(errOut, "compat-conformance:", err)
			return 1
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		work = tmp
	}
	rep, err := compat.SelfCheck(work)
	rep.Commit = GitCommit
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if encErr := enc.Encode(rep); encErr != nil {
		_, _ = fmt.Fprintln(errOut, "compat-conformance:", encErr)
		return 1
	}
	if err != nil {
		_, _ = fmt.Fprintln(errOut, "compat-conformance:", err)
		return 1
	}
	return 0
}

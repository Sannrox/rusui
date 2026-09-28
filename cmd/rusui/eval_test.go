package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/evalcorpus"
	"github.com/sannrox/rusui/internal/snapshot"
)

const evalDevCorpus = "../../eval/corpus/development.json"

// writeHeldOut writes an operator-owned held-out split of n comment cases,
// the first harmful of which are wrong comments.
func writeHeldOut(t *testing.T, dir string, n, harmful int) string {
	t.Helper()
	c := evalcorpus.Corpus{Version: evalcorpus.FormatVersion, Split: evalcorpus.SplitHeldOut,
		Clock: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Policy: `version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review]
  egress: trusted
projects:
  held:
    repos:
      example/held:
        visibility: public
        review: true
        comments: true
`}
	for i := range n {
		k := evalcorpus.Case{
			ID: fmt.Sprintf("H%d", i+1),
			Item: snapshot.Item{Repo: "example/held", Item: 1000 + i, ItemKind: "issue", State: "open",
				Title: "private held-out title", Body: "b", DefaultBranch: "main", MainSHA: "aaa",
				CreatedAt: "2026-09-01T00:00:00Z", UpdatedAt: "2026-09-01T00:00:00Z"},
			Result: engine.Artifact{Verdict: "propose_comment", Confidence: "high",
				ProposedActions: []engine.ProposedAction{{Type: "comment", ReasonCode: "note"}}},
			Judgment: evalcorpus.Judgment{Write: evalcorpus.WriteComment, Disposition: evalcorpus.DispositionUseful},
		}
		if i < harmful {
			k.Judgment = evalcorpus.Judgment{Write: evalcorpus.WriteNone, Disposition: evalcorpus.DispositionHarmful, WrongFinding: true}
		}
		c.Cases = append(c.Cases, k)
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, fmt.Sprintf("heldout-%d-%d.json", n, harmful))
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEvalCLIBlackbox(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "rusui")
	// No VCS stamp: with -revision given, the test does not depend on the
	// working tree's state.
	build := exec.Command("go", "build", "-buildvcs=false", "-o", bin, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	run := func(args ...string) (string, string, int) {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"eval", "-corpus", evalDevCorpus, "-revision", "blackbox"}, args...)...)
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return stdout.String(), stderr.String(), code
	}

	out, errOut, code := run()
	if code != 0 {
		t.Fatalf("development replay exit %d: %s", code, errOut)
	}
	for _, want := range []string{"## development split", "| E12 | protected_work | none | close |", "| comment | incomplete |"} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}

	good := writeHeldOut(t, dir, 30, 0)
	regressed := writeHeldOut(t, dir, 30, 12)
	if _, errOut, code := run("-heldout", good, "-gate", "comment"); code != evalExitGateIncomplete {
		t.Fatalf("baseline comment gate exit %d, want %d: %s", code, evalExitGateIncomplete, errOut)
	}
	out, errOut, code = run("-heldout", regressed, "-gate", "comment")
	if code != evalExitGateFailed || !strings.Contains(errOut, "comment gate fail") {
		t.Fatalf("regressed comment gate exit %d, want %d: %s", code, evalExitGateFailed, errOut)
	}
	if strings.Contains(out, "private held-out title") || strings.Contains(out, "| H1 |") {
		t.Fatalf("held-out content in report:\n%s", out)
	}

	// An exhausted review budget makes the engine report the repository;
	// none of that may reach stderr for a held-out split.
	var budget evalcorpus.Corpus
	raw, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &budget); err != nil {
		t.Fatal(err)
	}
	budget.Policy = strings.Replace(budget.Policy, "comments: true", "comments: true\n        max_reviews_per_repo_per_utc_day: 1", 1)
	budget.Cases = budget.Cases[:1]
	budget.Cases[0].Tags = []string{evalcorpus.TagDuplicateEvent}
	raw, err = json.Marshal(budget)
	if err != nil {
		t.Fatal(err)
	}
	exhausted := filepath.Join(dir, "heldout-budget.json")
	if err := os.WriteFile(exhausted, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errOut, _ := run("-heldout", exhausted); strings.Contains(errOut, "example/held") || strings.Contains(errOut, "H1") || strings.Contains(errOut, "private") {
		t.Fatalf("budget-exhausted held-out replay leaked to stderr:\n%s", errOut)
	}

	results := filepath.Join(dir, "results")
	if _, errOut, code := run("-heldout", good, "-out", results); code != 0 {
		t.Fatalf("preserve exit %d: %s", code, errOut)
	}
	if _, errOut, code := run("-heldout", good, "-out", results); code != 1 || !strings.Contains(errOut, "prior results are kept") {
		t.Fatalf("second write exit %d: %s", code, errOut)
	}
	saved, err := filepath.Glob(filepath.Join(results, "*.json"))
	if err != nil || len(saved) != 1 {
		t.Fatalf("preserved reports %v %v", saved, err)
	}
	if _, errOut, code := run("-heldout", good, "-check", saved[0]); code != 0 {
		t.Fatalf("same inputs check exit %d: %s", code, errOut)
	}
	if _, errOut, code := run("-heldout", regressed, "-check", saved[0]); code != evalExitStale || !strings.Contains(errOut, "stale evidence") {
		t.Fatalf("changed corpus check exit %d, want %d: %s", code, evalExitStale, errOut)
	}
}

func TestRevisionTrustRule(t *testing.T) {
	for _, tc := range []struct {
		name, override, commit, version, stamp, modified, want string
	}{
		{"override on a clean build", "abc", "unknown", "dev", "", "", "abc"},
		{"override on a dirty stamp", "abc", "unknown", "dev", "s1", "true", "abc-dirty"},
		{"override on a dirty version", "abc", "unknown", "v1-dirty", "", "", "abc-dirty"},
		{"make build, stamp agrees, clean", "", "c1", "dev", "c1", "false", "c1"},
		{"make build, stamp agrees, dirty", "", "c1", "dev", "c1", "true", "c1-dirty"},
		{"make build, stamp from enclosing checkout", "", "c1", "dev", "outer", "false", "c1-unverified"},
		{"make build without stamp", "", "c1", "dev", "", "", "c1-unverified"},
		{"plain go build, stamp only", "", "unknown", "dev", "s1", "false", "s1-unverified"},
		{"go run", "", "unknown", "dev", "", "", "unknown"},
	} {
		if got := revisionFrom(tc.override, tc.commit, tc.version, tc.stamp, tc.modified); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestPreserveReportKeepsRevisionPath(t *testing.T) {
	dir := t.TempDir()
	split := evalcorpus.SplitReport{Split: evalcorpus.SplitDevelopment, Digest: strings.Repeat("a", 64)}
	a, err := preserveReport(dir, evalcorpus.Report{Revision: "a/v1", Splits: []evalcorpus.SplitReport{split}}, []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{a: true}
	for _, rev := range []string{"b/v1", "a_v1", "a:v1"} {
		p, err := preserveReport(dir, evalcorpus.Report{Revision: rev, Splits: []evalcorpus.SplitReport{split}}, []byte("{}"))
		if err != nil {
			t.Fatalf("revision %q refused: %v", rev, err)
		}
		if paths[p] || filepath.Dir(p) != dir {
			t.Fatalf("revision %q wrote %s", rev, p)
		}
		paths[p] = true
	}
}

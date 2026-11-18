package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/server"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

// heldBack marks check content; it must never reach the plane.
const heldBack = "HELDBACK-7f3a"

const fixturePolicy = `version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
  review: true
  comments: true
  close: true
  implement: false
  land: false
projects:
  test:
    task_base: ancestor
    repos:
      example/test-repo:
        visibility: public
        review: true
        implement: true
`

// source is a real repository: a base, a later default tip, and two
// candidate heads on the base, one right and one wrong. rightTested is
// the right head plus the agent's own check.sh, a file the held-back test
// patch also creates.
type source struct {
	dir                                  string
	base, tip, right, wrong, rightTested string
}

func newSource(t *testing.T) source {
	t.Helper()
	s := source{dir: t.TempDir()}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = s.dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(file, body string) string {
		if err := os.WriteFile(filepath.Join(s.dir, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", file)
		git("commit", "-q", "-m", file)
		return git("rev-parse", "HEAD")
	}
	git("init", "-q", "-b", "main")
	s.base = commit("answer", "0\n")
	s.tip = commit("later", "unrelated\n")
	git("checkout", "-q", "--detach", s.base)
	s.right = commit("answer", "42\n")
	s.rightTested = commit("check.sh", "true # the agent's own check\n")
	git("checkout", "-q", "--detach", s.base)
	s.wrong = commit("answer", "41\n")
	return s
}

// guest answers claimed run turns by prompt, standing in for the agent.
// It records every claim, which is what a guest is given.
type guest struct {
	mu     sync.Mutex
	claims [][]byte
}

func (g *guest) serve(ctx context.Context, t *testing.T, e *engine.Engine, proposals map[string]string) {
	for ctx.Err() == nil {
		c, err := e.Claim("example/test-repo")
		if err != nil || c == nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		raw, _ := json.Marshal(c)
		g.mu.Lock()
		g.claims = append(g.claims, raw)
		g.mu.Unlock()
		r := &engine.TaskResult{SchemaVersion: engine.ResultSchema, SourceHash: c.ItemHash}
		if sha, ok := proposals[c.Snapshot.Body]; ok {
			// A live guest holds no GitHub credential: it proposes the
			// write and the plane publishes it once the write is applied.
			turn, err := store.GetTurn(e.Store, c.Job.ID)
			if err != nil {
				t.Error(err)
				return
			}
			r.CandidateSHA = sha
			r.Publish = &engine.PublishRequest{Branch: fmt.Sprintf("rusui/%d", turn.SessionID), Title: "set the answer"}
		} else {
			r.BlockedReason = "cannot reproduce"
		}
		// One model call through the plane's proxy: 120 tokens count, the
		// cache read does not. Usage the guest reports itself is ignored.
		grant := store.Grant{TurnID: c.Job.ID, LeaseGeneration: c.Job.LeaseGeneration, ExpiresAt: time.Now().Add(time.Hour)}
		status, usage := e.ModelResponseRecorder(grant, "https://model.invalid", "/v1/messages")
		status(200)
		usage(engine.ModelUsage{Input: 90, CacheWrite: 10, CacheRead: 500, Output: 20}, true)
		self := 1_000_000
		a := engine.Artifact{
			SchemaVersion: 1, Repo: c.Job.Repo, Item: c.Job.Item, ItemKind: c.Job.ItemKind,
			ClaimedRevision: c.Job.ClaimedRevision, SnapshotHash: c.ItemHash, MainSHA: c.Snapshot.MainSHA,
			Verdict: "keep", Confidence: "high", InputTokens: &self, OutputTokens: &self, Result: r,
		}
		if _, err := e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
			t.Error(err)
			return
		}
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A three-task corpus through a fixture plane: the right patch verifies,
// the wrong one fails its held-back check, and a blocked turn is not
// verified. Checks never reach the plane or a guest, a second run never
// replaces the report, and a changed check makes the report stale.
func TestCorpusRunScoresHeadsWithHeldBackChecks(t *testing.T) {
	src := newSource(t)
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "plane.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	pol, err := policy.Parse([]byte(fixturePolicy))
	if err != nil {
		t.Fatal(err)
	}
	f := gh.NewFake()
	// The forge's default tip has moved past every task's base.
	f.Put(snapshot.Item{Repo: "example/test-repo", Item: 1, ItemKind: "issue", State: "closed",
		MainSHA: src.tip, MergeCommitSHA: src.base, MergedIntoDefault: true})
	f.Put(snapshot.Item{Repo: "example/test-repo", Item: 11, ItemKind: "pull", State: "open", HeadSHA: src.right})
	f.Put(snapshot.Item{Repo: "example/test-repo", Item: 12, ItemKind: "pull", State: "open", HeadSHA: src.wrong})
	e := engine.New(st, pol, f, &clock.Fake{T: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)})
	e.Log = log.New(io.Discard, "", 0)
	e.Publisher = publisher{}
	e.ReloadPolicy(pol)
	hs := httptest.NewServer((&server.Server{Eng: e, WorkerSec: "wsec", OperatorTok: "op", Guest: "claude"}).Handler())
	t.Cleanup(hs.Close)
	ctx, cancel := context.WithCancel(context.Background())
	var g guest
	var wg sync.WaitGroup
	wg.Go(func() {
		g.serve(ctx, t, e, map[string]string{"set the answer": src.right, "set the answer too": src.wrong})
	})
	t.Cleanup(func() { cancel(); wg.Wait() })

	patch := "diff --git a/check.sh b/check.sh\nnew file mode 100644\n--- /dev/null\n+++ b/check.sh\n@@ -0,0 +1 @@\n+grep -qx 42 answer # " + heldBack + "\n"
	task := func(id, prompt string) map[string]any {
		return map[string]any{"id": id, "base_sha": src.base, "prompt": prompt,
			"allowed_paths": []string{"answer"}, "test_patch": patch, "checks": [][]string{{"sh", "check.sh"}}}
	}
	corpus := map[string]any{"version": 1, "project": "test", "repo": "example/test-repo", "ref": "main",
		"gate":  []string{"test", "-f", "answer"},
		"tasks": []any{task("right", "set the answer"), task("wrong", "set the answer too"), task("blocked", "explain the answer")}}
	corpusPath, policyPath, outDir := filepath.Join(dir, "corpus.json"), filepath.Join(dir, "policy.yaml"), filepath.Join(dir, "reports")
	writeJSON(t, corpusPath, corpus)
	if err := os.WriteFile(policyPath, []byte(fixturePolicy), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"-corpus", corpusPath, "-policy", policyPath, "-revision", "cand-1", "-url", hs.URL,
		"-worker-token", "wsec", "-token", "op", "-remote", src.dir, "-poll", "10ms", "-out", outDir}
	var out, errw bytes.Buffer
	if code := run(ctx, args, &out, &errw); code != 0 {
		t.Fatalf("run exit %d: %s", code, errw.String())
	}
	var rep Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rep.Tasks {
		got[r.ID] = r.Status
		// Only a failed head names the command that failed it.
		if failed := r.Status == StatusFailed; failed != strings.HasPrefix(r.Failure, "sh check.sh: exit status") {
			t.Fatalf("task %s failure %q", r.ID, r.Failure)
		}
	}
	want := map[string]string{"right": StatusVerified, "wrong": StatusFailed, "blocked": StatusNotVerified}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("statuses %v, want %v", got, want)
	}
	if rep.Revision != "cand-1" || len(rep.PolicyDigest) != 64 || len(rep.CorpusDigest) != 64 || rep.Limit == "" {
		t.Fatalf("report inputs %+v", rep)
	}
	if rep.Totals.TokensPerVerified == nil || *rep.Totals.TokensPerVerified != 360 {
		t.Fatalf("totals %+v", rep.Totals)
	}

	// Nothing the plane stored, and nothing a guest was handed, carries a check.
	g.mu.Lock()
	claims := bytes.Join(g.claims, nil)
	g.mu.Unlock()
	if len(g.claims) != 3 || bytes.Contains(claims, []byte(heldBack)) || bytes.Contains(claims, []byte("check.sh")) {
		t.Fatalf("guest saw a check in %d claims", len(g.claims))
	}
	if dump := dumpStore(t, st.DB); strings.Contains(dump, heldBack) || strings.Contains(dump, "check.sh") {
		t.Fatal("plane store holds check content")
	}

	// A rerun resumes the same tasks and keeps the existing report.
	reports, _ := filepath.Glob(filepath.Join(outDir, "*.json"))
	before, _ := os.ReadFile(reports[0])
	errw.Reset()
	if code := run(ctx, args, io.Discard, &errw); code != 1 || !strings.Contains(errw.String(), "prior results are kept") {
		t.Fatalf("rerun exit %d: %s", code, errw.String())
	}
	if after, _ := os.ReadFile(reports[0]); !bytes.Equal(before, after) || len(g.claims) != 3 {
		t.Fatalf("rerun replaced the report or readmitted tasks (%d claims)", len(g.claims))
	}

	// A changed check makes the prior report stale.
	corpus["tasks"].([]any)[2].(map[string]any)["checks"] = [][]string{{"sh", "check.sh"}, {"true"}}
	writeJSON(t, corpusPath, corpus)
	stale := append(args[:len(args)-2:len(args)-2], "-check", reports[0])
	errw.Reset()
	if code := run(ctx, stale, io.Discard, &errw); code != exitStale || !strings.Contains(errw.String(), "corpus changed") {
		t.Fatalf("changed check exit %d: %s", code, errw.String())
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.claims) != 3 {
		t.Fatalf("a stale check admitted tasks: %d claims", len(g.claims))
	}
}

// dumpStore renders every row of every table, for leak checks.
func dumpStore(t *testing.T, db *sql.DB) string {
	t.Helper()
	tables, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var n string
		if err := tables.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	_ = tables.Close()
	var b strings.Builder
	for _, n := range names {
		rows, err := db.Query(`SELECT * FROM "` + n + `"`)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for rows.Next() {
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for _, v := range vals {
				fmt.Fprintf(&b, "%s\n", v)
			}
		}
		_ = rows.Close()
	}
	return b.String()
}

// One check that already passes on the base invalidates the task, and no
// check sees the operator's plane credentials or home directory (checks
// run agent code).
// A head that edits a file the test patch touches still scores on the
// held-back version of that file: the patch replaces the agent's edit
// instead of failing to apply.
func TestScoreReplacesAgentEditsToHeldBackFiles(t *testing.T) {
	src := newSource(t)
	k := checker{remote: src.dir, workDir: t.TempDir(), timeout: time.Minute}
	patch := "diff --git a/check.sh b/check.sh\nnew file mode 100644\n--- /dev/null\n+++ b/check.sh\n@@ -0,0 +1 @@\n+grep -qx 42 answer\n"
	task := Task{ID: "t", BaseSHA: src.base, TestPatch: patch, Checks: [][]string{{"sh", "check.sh"}}}
	v, err := k.score(context.Background(), &Corpus{Repo: "example/test-repo"}, task, src.rightTested)
	if err != nil {
		t.Fatal(err)
	}
	if !v.headPass || v.basePass {
		t.Fatalf("verdict %+v: held-back check did not replace the agent's file", v)
	}
}

func TestScoreInvalidatesBasePassAndHidesOperatorSecrets(t *testing.T) {
	t.Setenv("RUSUI_OPERATOR_TOKEN", "op-secret")
	operatorHome := t.TempDir()
	t.Setenv("HOME", operatorHome)
	src := newSource(t)
	k := checker{remote: src.dir, workDir: t.TempDir(), timeout: time.Minute}
	c := &Corpus{Repo: "example/test-repo"}
	task := Task{ID: "t", BaseSHA: src.base, Checks: [][]string{
		{"grep", "-qx", "42", "answer"},
		{"sh", "-c", `test -z "$RUSUI_OPERATOR_TOKEN"`},
		{"sh", "-c", `test "$HOME" != "$1" && test -d "$HOME"`, "sh", operatorHome},
	}}
	v, err := k.score(context.Background(), c, task, src.right)
	if err != nil {
		t.Fatal(err)
	}
	if !v.headPass || !v.basePass || !v.gatePass {
		t.Fatalf("verdict %+v: secret visible to a check, or base pass missed", v)
	}
}

// A check whose child outlives it and holds the output pipe still ends at
// the check timeout.
func TestCheckTimeoutEndsTheWholeCheck(t *testing.T) {
	k := checker{timeout: 200 * time.Millisecond}
	start := time.Now()
	if _, err := k.run(context.Background(), t.TempDir(), "", []string{"sh", "-c", "sleep 30 & sleep 30"}); err == nil {
		t.Fatal("timed-out check passed")
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("check ran %v past its timeout", d)
	}
}

// A check that passes but leaves a child holding its output still passes,
// promptly, and the child does not outlive it.
func TestPassingCheckLeavesNothingRunning(t *testing.T) {
	dir := t.TempDir()
	k := checker{workDir: t.TempDir(), timeout: time.Minute}
	start := time.Now()
	if _, err := k.run(context.Background(), dir, "", []string{"sh", "-c", "(sleep 1; touch survived) & exit 0"}); err != nil {
		t.Fatalf("passing check: %v", err)
	}
	if d := time.Since(start); d > 900*time.Millisecond {
		t.Fatalf("waited %v on a child", d)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(dir, "survived")); err == nil {
		t.Fatal("child outlived its check")
	}
}

// A check that leaves a read-only tree in its scratch home (as Go's
// module cache does) is still cleaned up.
func TestRemoveDeletesReadOnlyTrees(t *testing.T) {
	work := t.TempDir()
	k := checker{workDir: work, timeout: time.Minute}
	dir := filepath.Join(work, "checkout")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := k.run(context.Background(), dir, "", []string{"sh", "-c", `mkdir -p "$HOME/mod/x" && touch "$HOME/mod/x/f" && chmod -R a-w "$HOME/mod"`}); err != nil {
		t.Fatal(err)
	}
	k.remove(dir)
	if left, _ := os.ReadDir(work); len(left) != 0 {
		t.Fatalf("left behind %v", left)
	}
}

// publisher stands in for the plane's App publisher: the proposed head
// becomes pull request 11.
type publisher struct{}

func (publisher) PublishPull(string, gh.PullSpec) (int, error) { return 11, nil }
func (publisher) PushBase(string, string, string) error        { return nil }

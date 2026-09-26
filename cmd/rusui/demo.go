package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/runner"
	"github.com/sannrox/rusui/internal/server"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

const (
	demoRepo    = "sample/demo"
	demoProject = "sample"
	demoPolicy  = `version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
  review: true
  comments: false
  close: false
  implement: false
  land: false
  max_reviews_per_repo_per_utc_day: 50
projects:
  sample:
    repos:
      sample/demo:
        visibility: public
        review: true
        comments: false
        close: false
        implement: false
        land: false
`
)

func demoCLI(args []string) {
	os.Exit(demoMain(args, os.Stdout, os.Stderr))
}

func demoMain(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	fs.SetOutput(errw)
	dir := fs.String("dir", "", "isolated sample state directory (default: a new temp directory)")
	keep := fs.Bool("keep", false, "leave sample state on disk")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(errw, "usage: rusui demo [-dir DIR] [-keep]")
		return 2
	}
	stateDir, err := demoStateDir(*dir)
	if err != nil {
		_, _ = fmt.Fprintln(errw, "demo:", err)
		return 1
	}
	if !*keep {
		defer func() { _ = os.RemoveAll(stateDir) }()
	}
	if err := runSampleDemo(stateDir, out); err != nil {
		_, _ = fmt.Fprintln(errw, "demo:", err)
		return 1
	}
	if !*keep {
		_, _ = fmt.Fprintf(out, "removed sample state %s\n", stateDir)
	} else {
		_, _ = fmt.Fprintf(out, "kept sample state %s\n", stateDir)
	}
	return 0
}

func demoStateDir(dir string) (string, error) {
	if dir == "" {
		return os.MkdirTemp("", "rusui-sample-demo-*")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err == nil && !info.IsDir() {
		return "", fmt.Errorf("state parent %s is not a directory", abs)
	}
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(abs, "rusui-sample-demo-*")
}

func runSampleDemo(stateDir string, out io.Writer) error {
	if err := os.WriteFile(filepath.Join(stateDir, "SAMPLE"), []byte("rusui sample demo data; not a production plane\n"), 0o600); err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(stateDir, "sample.db"))
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	pol, err := policy.Parse([]byte(demoPolicy))
	if err != nil {
		return err
	}
	fake := gh.NewFake()
	item := snapshot.Item{
		Repo: demoRepo, Item: 1, ItemKind: "issue", State: "open",
		Title: "SAMPLE: demo issue", Body: "This fixture never leaves the sample plane.",
		Labels: []string{"sample"}, UpdatedAt: "2026-01-01T00:00:00Z",
		CreatedAt: "2026-01-01T00:00:00Z", DefaultBranch: "main", MainSHA: "sample",
	}
	fake.Put(item)
	e := engine.New(st, pol, fake, clock.Real{})
	e.ReloadPolicy(pol)
	e.Env = env.Process{Root: filepath.Join(stateDir, "environments")}
	e.SnapshotRoot = filepath.Join(stateDir, "snapshots")
	if err := e.CatchUpItem(item.Repo, item.Item, item.ItemKind); err != nil {
		return err
	}
	ok, err := e.StepRefresh()
	if err != nil {
		return fmt.Errorf("admit sample item: %w", err)
	}
	if !ok {
		return fmt.Errorf("admit sample item: not admitted")
	}
	secret, err := randomSecret()
	if err != nil {
		return err
	}
	hs := httptest.NewServer((&server.Server{Eng: e, WorkerSec: secret, OperatorTok: secret, WebhookSec: secret, SlackSec: secret}).Handler())
	defer hs.Close()

	claimed, err := e.Claim(demoRepo)
	if err != nil {
		return fmt.Errorf("claim sample turn: %w", err)
	}
	if claimed == nil {
		return fmt.Errorf("claim sample turn: no queued work")
	}
	art := engine.Artifact{
		SchemaVersion: 1, Repo: claimed.Job.Repo, Item: claimed.Job.Item, ItemKind: claimed.Job.ItemKind,
		ClaimedRevision: claimed.Job.ClaimedRevision, SnapshotHash: claimed.ItemHash, MainSHA: claimed.Snapshot.MainSHA,
		Verdict: "keep", Confidence: "high",
		Publishable: map[string]any{"sample": true, "label": "SAMPLE"},
	}
	raw, err := json.Marshal(art)
	if err != nil {
		return err
	}
	if _, err := e.Fail(claimed.Job.ID, claimed.Job.LeaseGeneration, claimed.Job.ClaimedRevision); err != nil {
		return err
	}
	script, err := runner.DriverScript(stateDir, string(raw))
	if err != nil {
		return err
	}
	cli := &runner.Client{Base: hs.URL, Bootstrap: secret, Repo: demoRepo, Name: "sample-demo"}
	if err := runner.OneTurn(context.Background(), cli, []string{script}); err != nil {
		return err
	}
	n, err := store.CountReceipts(st, claimed.Job.ID)
	if err != nil {
		return fmt.Errorf("sample receipt missing: %w", err)
	}
	if n < 1 {
		return fmt.Errorf("sample receipt missing")
	}
	payload, err := store.LatestReviewJSON(st, claimed.Job.ID)
	if err != nil {
		return fmt.Errorf("sample review missing: %w", err)
	}
	if payload == "" {
		return fmt.Errorf("sample review missing")
	}
	turn, err := store.GetTurn(st, claimed.Job.ID)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "SAMPLE rusui demo — this is not a real review")
	_, _ = fmt.Fprintf(out, "Project: %s | Session: %d | Turn: %d | Item: %s#%d\n", demoProject, turn.SessionID, turn.ID, demoRepo, item.Item)
	_, _ = fmt.Fprintf(out, "Receipt: complete | Review payload:\n%s\n", payload)
	return nil
}

func randomSecret() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

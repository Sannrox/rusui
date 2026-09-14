package runner_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/runner"
	"github.com/sannrox/rusui/internal/server"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

const fixture = `version: 1
defaults:
  never_release: true
  never_leak_private_to_public: true
  review: true
  comments: true
  close: true
  implement: false
  land: false
  max_reviews_per_repo_per_utc_day: 50
repos:
  example/test-repo:
    visibility: public
    review: true
    comments: true
    close: true
`

func setup(t *testing.T) (*engine.Engine, *store.Store, *httptest.Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	pol, err := policy.Parse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.Real{}
	f := gh.NewFake()
	it := snapshot.Item{
		Repo: "example/test-repo", Item: 1, ItemKind: "issue", State: "open",
		Title: "bug", Body: "repro", Labels: []string{"bug"}, UpdatedAt: "2026-01-01T00:00:00Z",
		CreatedAt: "2025-01-01T00:00:00Z", LastNonBotCommentAt: "2025-01-02T00:00:00Z",
		DefaultBranch: "main", MainSHA: "aaa",
	}
	f.Put(it)
	e := engine.New(st, pol, f, clk)
	e.ReloadPolicy(pol)
	if err := e.CatchUpItem(it.Repo, it.Item, it.ItemKind); err != nil {
		t.Fatal(err)
	}
	ok, err := e.StepRefresh()
	if err != nil || !ok {
		t.Fatalf("refresh %v %v", ok, err)
	}
	srv := &server.Server{Eng: e, WebhookSec: "whsec", WorkerSec: "wsec", SlackSec: "slsec", SlackUsers: map[string]bool{"U1": true}}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return e, st, hs
}

func TestProcessDriverCompletesWithReceipt(t *testing.T) {
	e, st, hs := setup(t)
	c0, err := e.Claim("example/test-repo")
	if err != nil || c0 == nil {
		t.Fatalf("preclaim %v %v", c0, err)
	}
	// Release by failing so the runner can claim a fresh lease after we
	// re-queue. Simpler: build artifact from this claim then fail and
	// admit again? Claim already consumed the job. Fail it and operator
	// retry, or just use the first claim's snapshot to write the driver
	// and complete via runner after fail+retry.
	if _, err := e.Fail(c0.Job.ID, c0.Job.LeaseGeneration, c0.Job.ClaimedRevision); err != nil {
		t.Fatal(err)
	}
	art := engine.Artifact{
		SchemaVersion: 1, Repo: c0.Job.Repo, Item: c0.Job.Item, ItemKind: c0.Job.ItemKind,
		ClaimedRevision: c0.Job.ClaimedRevision, SnapshotHash: c0.ItemHash, MainSHA: c0.Snapshot.MainSHA,
		Verdict: "keep", Confidence: "high",
	}
	raw, _ := json.Marshal(art)
	script, err := runner.DriverScript(t.TempDir(), string(raw))
	if err != nil {
		t.Fatal(err)
	}
	cli := &runner.Client{Base: hs.URL, Bootstrap: "wsec", Repo: "example/test-repo", Name: "local"}
	if err := runner.OneTurn(context.Background(), cli, []string{script}); err != nil {
		t.Fatal(err)
	}
	n, err := store.CountReceipts(st, c0.Job.ID)
	if err != nil || n < 1 {
		t.Fatalf("receipts %d %v", n, err)
	}
}

func TestDeadlineKillsProcessGroup(t *testing.T) {
	e, _, hs := setup(t)
	e.ExecDeadline = 200 * time.Millisecond
	cli := &runner.Client{Base: hs.URL, Bootstrap: "wsec", Repo: "example/test-repo", Name: "local"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := runner.OneTurn(ctx, cli, []string{"sleep", "30"})
	if err == nil {
		t.Fatal("expected deadline kill")
	}
	if !strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "context") {
		t.Fatalf("err %v", err)
	}
}

func TestDriverEnvHasTurnTokenNotPlaneSecret(t *testing.T) {
	a := &runner.Assignment{TurnID: 9, TurnToken: "tok"}
	env := runner.DriverEnv(a, "/tmp/home", "/bin")
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "RUSUI_TURN_TOKEN=tok") {
		t.Fatal(joined)
	}
	if strings.Contains(joined, "wsec") || strings.Contains(joined, "WORKER") {
		t.Fatal(joined)
	}
	t.Setenv("RUSUI_WORKER_SECRET", "wsec")
	for _, e := range env {
		if strings.Contains(e, "wsec") {
			t.Fatal(e)
		}
	}
}

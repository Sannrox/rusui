package runner_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/acp"
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

const fixture = `version: 2
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
  max_reviews_per_repo_per_utc_day: 50
projects:
  test:
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
	if !strings.Contains(joined, "RUSUI_TURN_TOKEN=tok") || !strings.Contains(joined, "XAI_API_KEY=tok") {
		t.Fatal(joined)
	}
	a.ModelBaseURL = "http://rusui.plane:8080/model-proxy"
	joined = strings.Join(runner.DriverEnv(a, "/tmp/home", "/bin"), "\n")
	if !strings.Contains(joined, "GROK_XAI_API_BASE_URL=http://rusui.plane:8080/model-proxy") {
		t.Fatal(joined)
	}
	a.GitProxyURL = "http://rusui.plane:8080/git-proxy/github.com/"
	joined = strings.Join(runner.DriverEnv(a, "/tmp/home", "/bin"), "\n")
	if !strings.Contains(joined, "url.http://rusui.plane:8080/git-proxy/github.com/.insteadof") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "Authorization: Bearer tok") {
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

func TestACPTurnUsesProvisionedWorkspace(t *testing.T) {
	e, _, hs := setup(t)
	root := t.TempDir()
	e.Env = env.Process{Root: root}
	cli := &runner.Client{Base: hs.URL, Bootstrap: "wsec", Repo: "example/test-repo", Name: "local"}
	var gotDir, gotWorkspace string
	err := runner.OneACPTurn(context.Background(), cli, func(a *runner.Assignment, dir string) (*acp.Client, func(), error) {
		gotDir = dir
		gotWorkspace = a.Workspace
		if a.Driver != env.KindProcess {
			t.Fatalf("driver %q", a.Driver)
		}
		return startFakeACP(t, runner.HTTPRecorder{Base: hs.URL, Token: a.TurnToken, TurnID: a.TurnID})
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotWorkspace == "" || !strings.HasPrefix(gotWorkspace, root) {
		t.Fatalf("workspace %q", gotWorkspace)
	}
	if gotDir != gotWorkspace {
		t.Fatalf("host dir %q workspace %q", gotDir, gotWorkspace)
	}
}

func TestWorkspaceForContainerHasNoHostCwd(t *testing.T) {
	dir, tmp, err := runner.WorkspaceFor(&runner.Assignment{Driver: "container", Handle: "ctr-1"})
	if err != nil || tmp || dir != "" {
		t.Fatalf("%q tmp=%v %v", dir, tmp, err)
	}
	dir, tmp, err = runner.WorkspaceFor(&runner.Assignment{Workspace: "/ws"})
	if err != nil || tmp || dir != "/ws" {
		t.Fatalf("%q tmp=%v %v", dir, tmp, err)
	}
}

func TestGrokHostExecsInContainer(t *testing.T) {
	e, st, hs := setup(t)
	rt := &env.FakeRuntime{}
	rt.StdioHook = func(handle string, argv, env []string) (io.WriteCloser, io.ReadCloser, func(), error) {
		return startFakeACPStdio(t)
	}
	e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	cli := &runner.Client{Base: hs.URL, Bootstrap: "wsec", Repo: "example/test-repo", Name: "local", Exec: rt}
	if err := runner.OneACPTurn(context.Background(), cli, runner.GrokHost(cli)); err != nil {
		t.Fatal(err)
	}
	if len(rt.Stdio) != 1 || rt.Stdio[0].Handle == "" {
		t.Fatalf("stdio %#v", rt.Stdio)
	}
	joined := strings.Join(rt.Stdio[0].Argv, " ")
	if !strings.Contains(joined, "agent stdio") || !strings.Contains(joined, "--permission-mode default") {
		t.Fatalf("argv %q", joined)
	}
	envj := strings.Join(rt.Stdio[0].Env, "\n")
	if !strings.Contains(envj, "XAI_API_KEY=") {
		t.Fatal(envj)
	}
	var turnID int64
	if err := st.DB.QueryRow(`SELECT id FROM turns WHERE state='completed'`).Scan(&turnID); err != nil {
		t.Fatal(err)
	}
}

func startFakeACPStdio(t *testing.T) (io.WriteCloser, io.ReadCloser, func(), error) {
	t.Helper()
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = (&acp.FakeAgent{In: agentIn, Out: agentOut}).Run()
	}()
	stop := func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
		<-done
	}
	return clientOut, clientIn, stop, nil
}

func TestACPHostCompletesWithReceipts(t *testing.T) {
	_, st, hs := setup(t)
	cli := &runner.Client{Base: hs.URL, Bootstrap: "wsec", Repo: "example/test-repo", Name: "local"}
	err := runner.OneACPTurn(context.Background(), cli, func(a *runner.Assignment, dir string) (*acp.Client, func(), error) {
		return startFakeACP(t, runner.HTTPRecorder{Base: hs.URL, Token: a.TurnToken, TurnID: a.TurnID})
	})
	if err != nil {
		t.Fatal(err)
	}
	acts, err := store.ListActions(st, "example/test-repo", 1)
	if err != nil || len(acts) == 0 {
		t.Fatalf("actions %d %v", len(acts), err)
	}
	var turnID int64
	if err := st.DB.QueryRow(`SELECT id FROM turns WHERE state='completed'`).Scan(&turnID); err != nil {
		t.Fatal(err)
	}
	n, err := store.CountReceipts(st, turnID)
	if err != nil || n < 1 {
		t.Fatalf("receipts %d %v", n, err)
	}
}

func startFakeACP(t *testing.T, rec acp.Recorder) (*acp.Client, func(), error) {
	t.Helper()
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = (&acp.FakeAgent{In: agentIn, Out: agentOut}).Run()
	}()
	stop := func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
		<-done
	}
	return &acp.Client{In: clientIn, Out: clientOut, Rec: rec, Perm: acp.DenyUnmatched{}}, stop, nil
}

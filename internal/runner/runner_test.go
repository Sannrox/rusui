package runner_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
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
	"github.com/sannrox/rusui/internal/provider"
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
  other:
    repos:
      example/other-repo:
        visibility: public
`

func fixtureItem(repo string, item int) snapshot.Item {
	return snapshot.Item{
		Repo: repo, Item: item, ItemKind: "issue", State: "open",
		Title: "bug", Body: "repro", Labels: []string{"bug"}, UpdatedAt: "2026-01-01T00:00:00Z",
		CreatedAt: "2025-01-01T00:00:00Z", LastNonBotCommentAt: "2025-01-02T00:00:00Z",
		DefaultBranch: "main", MainSHA: "aaa",
	}
}

func setup(t *testing.T, opts ...func(*server.Server)) (*engine.Engine, *store.Store, *httptest.Server) {
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
	it := fixtureItem("example/test-repo", 1)
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
	for _, o := range opts {
		o(srv)
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return e, st, hs
}

func enqueueItem(t *testing.T, e *engine.Engine, item snapshot.Item) {
	t.Helper()
	f, ok := e.GitHub.(*gh.Fake)
	if !ok {
		t.Fatal("engine does not use the fake GitHub client")
	}
	f.Put(item)
	if err := e.CatchUpItem(item.Repo, item.Item, item.ItemKind); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.StepRefresh(); err != nil || !ok {
		t.Fatalf("refresh %s#%d: ok=%v err=%v", item.Repo, item.Item, ok, err)
	}
}

func runnerArtifact(a *runner.Assignment) engine.Artifact {
	return engine.Artifact{
		SchemaVersion:   1,
		Repo:            a.Repo,
		Item:            a.Item,
		ItemKind:        a.ItemKind,
		ClaimedRevision: a.ClaimedRevision,
		SnapshotHash:    a.ItemHash,
		MainSHA:         "aaa",
		Verdict:         "keep",
		Confidence:      "high",
	}
}

func TestMultiRepoClaimFairnessAndWorkspaceIsolation(t *testing.T) {
	e, st, hs := setup(t)
	e.Env = env.Process{Root: t.TempDir()}
	busy, err := e.Claim("example/test-repo")
	if err != nil || busy == nil {
		t.Fatalf("initial claim %v %v", busy, err)
	}
	itemA := fixtureItem("example/test-repo", 2)
	itemB := fixtureItem("example/other-repo", 1)
	enqueueItem(t, e, itemA)
	enqueueItem(t, e, itemB)

	cli := &runner.Client{
		Base: hs.URL, Bootstrap: "wsec", Name: "multi",
		Repos: []string{"example/test-repo", "example/other-repo"},
	}
	claimedB, err := cli.Claim()
	if err != nil || claimedB == nil || claimedB.Repo != itemB.Repo {
		t.Fatalf("claim past busy repository: assignment=%+v err=%v", claimedB, err)
	}
	if err := cli.Complete(claimedB, runnerArtifact(claimedB)); err != nil {
		t.Fatalf("complete repository B: %v", err)
	}
	turn, err := store.GetTurn(st, busy.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.CancelSession(turn.SessionID); err != nil {
		t.Fatalf("release repository A capacity: %v", err)
	}

	claimedA, err := cli.Claim()
	if err != nil || claimedA == nil || claimedA.Repo != itemA.Repo || claimedA.Item != itemA.Item {
		t.Fatalf("claim repository A after capacity frees: assignment=%+v err=%v", claimedA, err)
	}
	if claimedA.SessionID == claimedB.SessionID || claimedA.Workspace == "" || claimedA.Workspace == claimedB.Workspace {
		t.Fatalf("repositories shared session/workspace: A=%+v B=%+v", claimedA, claimedB)
	}
	if err := cli.Complete(claimedA, runnerArtifact(claimedA)); err != nil {
		t.Fatalf("complete repository A: %v", err)
	}
	for _, assignment := range []*runner.Assignment{claimedA, claimedB} {
		session, err := store.GetSession(st, assignment.SessionID)
		if err != nil || session.Repo != assignment.Repo || session.EnvironmentID == 0 {
			t.Fatalf("session for assignment %+v: session=%+v err=%v", assignment, session, err)
		}
	}
}

func TestMultiRepoClaimFailsClosedForUnboundRepository(t *testing.T) {
	_, _, hs := setup(t)
	cli := &runner.Client{
		Base: hs.URL, Bootstrap: "wsec", Name: "multi",
		Repos: []string{"example/unbound", "example/test-repo"},
	}
	if assignment, err := cli.Claim(); err == nil || assignment != nil {
		t.Fatalf("unbound repository claim assignment=%+v err=%v", assignment, err)
	}
	assignment, err := cli.Claim()
	if err != nil || assignment == nil || assignment.Repo != "example/test-repo" {
		t.Fatalf("claim after unbound repository refusal: assignment=%+v err=%v", assignment, err)
	}
}

func TestMultiRepoClaimSkipsPausedProject(t *testing.T) {
	e, _, hs := setup(t)
	if err := e.SetPause("test", true); err != nil {
		t.Fatal(err)
	}
	item := fixtureItem("example/other-repo", 1)
	enqueueItem(t, e, item)
	cli := &runner.Client{
		Base: hs.URL, Bootstrap: "wsec", Name: "multi",
		Repos: []string{"example/test-repo", "example/other-repo"},
	}
	assignment, err := cli.Claim()
	if err != nil || assignment == nil || assignment.Repo != item.Repo {
		t.Fatalf("claim past paused project: assignment=%+v err=%v", assignment, err)
	}
}

func TestIdleMultiRepoClaimUsesOneRoundTrip(t *testing.T) {
	var n int
	var gotRepos []string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		var req struct {
			Repo  string   `json:"repo"`
			Repos []string `json:"repos"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		gotRepos = req.Repos
		if r.URL.Path != "/jobs/claim" || req.Repo != "" || len(req.Repos) != 3 {
			t.Errorf("path=%s repo=%q repos=%v", r.URL.Path, req.Repo, req.Repos)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(hs.Close)
	cli := &runner.Client{
		Base: hs.URL, Bootstrap: "wsec",
		Repos: []string{"example/one", "example/two", "example/three"},
	}
	assignment, err := cli.Claim()
	if err != nil || assignment != nil {
		t.Fatalf("idle claim assignment=%+v err=%v", assignment, err)
	}
	if n != 1 {
		t.Fatalf("idle multi-repo claim round-trips=%d", n)
	}
	if len(gotRepos) != 3 {
		t.Fatalf("repos payload %v", gotRepos)
	}
	assignment, err = cli.Claim()
	if err != nil || assignment != nil || n != 2 {
		t.Fatalf("second idle poll round-trips=%d assignment=%+v err=%v", n, assignment, err)
	}
}

func TestSingleRepoClaimKeepsRepoField(t *testing.T) {
	var n int
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		var req struct {
			Repo  string   `json:"repo"`
			Repos []string `json:"repos"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		if req.Repo != "example/test-repo" || len(req.Repos) != 0 {
			t.Errorf("single-repo body repo=%q repos=%v", req.Repo, req.Repos)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(hs.Close)
	cli := &runner.Client{Base: hs.URL, Bootstrap: "wsec", Repo: "example/test-repo"}
	if assignment, err := cli.Claim(); err != nil || assignment != nil || n != 1 {
		t.Fatalf("single-repo idle n=%d assignment=%+v err=%v", n, assignment, err)
	}
}

func TestClaimWithOutcomeClassifiesNoWorkReasons(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		blocked    string
		body       string
		wantReason string
		wantError  bool
	}{
		{name: "empty queue", status: http.StatusNoContent, wantReason: "no queued turn"},
		{name: "policy", status: http.StatusConflict, body: "policy\n", wantReason: "policy or lane disabled", wantError: true},
		{name: "lease cap", status: http.StatusConflict, blocked: "budget", body: "budget\n", wantReason: "lease cap or review budget", wantError: true},
		{name: "paused", status: http.StatusConflict, blocked: "paused", body: "paused\n", wantReason: "paused", wantError: true},
		{name: "other conflict", status: http.StatusConflict, body: "environment setup failed", wantReason: "claim failed (HTTP 409)", wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.blocked != "" {
					w.Header().Set("X-Rusui-Claim-Blocked", tc.blocked)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer hs.Close()
			cli := &runner.Client{Base: hs.URL, Bootstrap: "worker", Repo: "example/repo"}
			outcome, err := cli.ClaimWithOutcome()
			if (err != nil) != tc.wantError {
				t.Fatalf("claim error = %v, want error %v", err, tc.wantError)
			}
			if tc.body != "" && (err == nil || !strings.Contains(err.Error(), strings.TrimSpace(tc.body))) {
				t.Fatalf("claim error lost response detail: %v", err)
			}
			if outcome.Assignment != nil || len(outcome.NoWorkReasons) != 1 || outcome.NoWorkReasons[0] != tc.wantReason {
				t.Fatalf("claim outcome = %+v, want no-work reason %q", outcome, tc.wantReason)
			}
		})
	}
}

func TestClientRejectsAssignmentOutsideConfiguredRepositories(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs/claim" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(runner.Assignment{TurnID: 1, Repo: "example/other-repo"})
	}))
	defer hs.Close()
	cli := &runner.Client{Base: hs.URL, Bootstrap: "wsec", Repo: "example/test-repo"}
	if assignment, err := cli.Claim(); err == nil || assignment != nil {
		t.Fatalf("out-of-set assignment=%+v err=%v", assignment, err)
	}
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
	for _, banned := range []string{"AWS_SECRET", "NPM_TOKEN", "DOCKER_TOKEN"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("injected secret %s in\n%s", banned, joined)
		}
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
		return startFakeACP(t, &runner.HTTPRecorder{Base: hs.URL, Token: a.TurnToken, TurnID: a.TurnID})
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
	done := make(chan struct{})
	go allowPendingApprovals(t, hs.URL, done)
	if err := runner.OneACPTurn(context.Background(), cli, runner.GuestHost(cli)); err != nil {
		t.Fatal(err)
	}
	close(done)
	if len(rt.Stdio) != 1 || rt.Stdio[0].Handle == "" {
		t.Fatalf("stdio %#v", rt.Stdio)
	}
	if len(rt.Links) != 1 || rt.Links[0].Handle != rt.Stdio[0].Handle {
		t.Fatalf("turn ran without its guest link: %#v", rt.Links)
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

func TestClaudeGuestExecsAdapterInContainer(t *testing.T) {
	e, st, hs := setup(t, func(s *server.Server) { s.Guest = acp.GuestClaude })
	rt := &env.FakeRuntime{}
	rt.StdioHook = func(handle string, argv, env []string) (io.WriteCloser, io.ReadCloser, func(), error) {
		return startFakeClaudeStdio(t)
	}
	e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	cli := &runner.Client{Base: hs.URL, Bootstrap: "wsec", Repo: "example/test-repo", Name: "local", Exec: rt}
	done := make(chan struct{})
	go allowPendingApprovals(t, hs.URL, done)
	if err := runner.OneACPTurn(context.Background(), cli, runner.GuestHost(cli)); err != nil {
		t.Fatal(err)
	}
	close(done)
	if len(rt.Stdio) != 1 || strings.Join(rt.Stdio[0].Argv, " ") != acp.ClaudeStdio {
		t.Fatalf("stdio %#v", rt.Stdio)
	}
	envj := strings.Join(rt.Stdio[0].Env, "\n")
	for _, want := range []string{"ANTHROPIC_AUTH_TOKEN=", "ANTHROPIC_BASE_URL=https://rusui.plane", "CLAUDE_CONFIG_DIR=/tmp/rusui-claude"} {
		if !strings.Contains(envj, want) {
			t.Fatalf("missing %s in\n%s", want, envj)
		}
	}
	if strings.Contains(envj, "XAI_API_KEY") || strings.Contains(envj, "GROK_") {
		t.Fatalf("grok env in claude guest:\n%s", envj)
	}
	var n int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM turns WHERE state='completed'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("completed turns %d %v", n, err)
	}
}

func TestClaudeGuestEnvHoldsOnlyTheGrant(t *testing.T) {
	a := &runner.Assignment{TurnID: 9, TurnToken: "grant", Guest: acp.GuestClaude, ModelBaseURL: "http://127.0.0.1:8080/model-proxy"}
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-operator")
	t.Setenv("CLAUDE_CONFIG_DIR", "/Users/op/.claude")
	joined := strings.Join(runner.DriverEnv(a, "/tmp/home", "/bin"), "\n")
	if !strings.Contains(joined, "ANTHROPIC_AUTH_TOKEN=grant") || !strings.Contains(joined, "CLAUDE_CONFIG_DIR=/tmp/home/.rusui-claude") {
		t.Fatal(joined)
	}
	if strings.Contains(joined, "sk-ant-operator") || strings.Contains(joined, "/Users/op/.claude") {
		t.Fatalf("operator claude credential or login leaked:\n%s", joined)
	}
	if strings.Contains(joined, "ANTHROPIC_MODEL=") || strings.Contains(joined, "ANTHROPIC_DEFAULT_") {
		t.Fatalf("unnamed guest must not invent a model:\n%s", joined)
	}
	if !strings.Contains(joined, "ANTHROPIC_API_KEY=\n") && !strings.HasSuffix(joined, "ANTHROPIC_API_KEY=") {
		t.Fatalf("claude guest must clear ANTHROPIC_API_KEY:\n%s", joined)
	}
}

func TestClaudeGuestEnvCarriesConfiguredModel(t *testing.T) {
	a := &runner.Assignment{
		TurnID: 9, TurnToken: "grant", Guest: acp.GuestClaude,
		ModelBaseURL: "http://127.0.0.1:8080/model-proxy", GuestModel: "grok-4.6",
	}
	joined := strings.Join(runner.DriverEnv(a, "/tmp/home", "/bin"), "\n")
	for _, key := range []string{
		"ANTHROPIC_MODEL=grok-4.6",
		"ANTHROPIC_DEFAULT_OPUS_MODEL=grok-4.6",
		"ANTHROPIC_DEFAULT_SONNET_MODEL=grok-4.6",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL=grok-4.6",
		"ANTHROPIC_AUTH_TOKEN=grant",
	} {
		if !strings.Contains(joined, key) {
			t.Fatalf("missing %s in\n%s", key, joined)
		}
	}
	grok := &runner.Assignment{
		TurnID: 3, TurnToken: "grant", Guest: acp.GuestGrok,
		ModelBaseURL: "http://127.0.0.1:9/model-proxy", GuestModel: "grok-4.6",
	}
	grokEnv := strings.Join(runner.DriverEnv(grok, "/tmp/home", "/bin"), "\n")
	if strings.Contains(grokEnv, "ANTHROPIC_") {
		t.Fatalf("grok guest received claude model env:\n%s", grokEnv)
	}
	if !strings.Contains(grokEnv, "XAI_API_KEY=grant") || !strings.Contains(grokEnv, "GROK_XAI_API_BASE_URL=http://127.0.0.1:9/model-proxy") {
		t.Fatalf("grok env:\n%s", grokEnv)
	}
	argv, err := acp.SpawnArgsFor(acp.GuestGrok)
	if err != nil || strings.Join(argv, " ") != acp.GrokStdio {
		t.Fatalf("grok spawn %v %v", argv, err)
	}
	a.GuestModel = "claude opus"
	joined = strings.Join(runner.DriverEnv(a, "/tmp/home", "/bin"), "\n")
	if strings.Contains(joined, "ANTHROPIC_MODEL=") {
		t.Fatalf("invalid model id copied into the guest:\n%s", joined)
	}
}

func TestUnknownGuestFailsClosed(t *testing.T) {
	if _, err := acp.SpawnArgsFor("cursor"); err == nil {
		t.Fatal("unknown guest accepted")
	}
	if argv, err := acp.SpawnArgsFor(""); err != nil || strings.Join(argv, " ") != acp.GrokStdio {
		t.Fatalf("default %v %v", argv, err)
	}
}

func startFakeClaudeStdio(t *testing.T) (io.WriteCloser, io.ReadCloser, func(), error) {
	t.Helper()
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(agentIn)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		if !sc.Scan() {
			return
		}
		// Claude Code 2.1.283 emits system/init only after the first input.
		raw, _ := json.Marshal(map[string]any{
			"type":                "system",
			"subtype":             "init",
			"claude_code_version": provider.ClaudeCodeVersion,
			"session_id":          "claude-sess",
		})
		_, _ = agentOut.Write(append(raw, '\n'))
		var msg map[string]any
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil || msg["type"] != "user" {
			return
		}
		raw, _ = json.Marshal(map[string]any{"type": "assistant", "text": "hello-claude"})
		_, _ = agentOut.Write(append(raw, '\n'))
		raw, _ = json.Marshal(map[string]any{"type": "result"})
		_, _ = agentOut.Write(append(raw, '\n'))
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
		return startFakeACP(t, &runner.HTTPRecorder{Base: hs.URL, Token: a.TurnToken, TurnID: a.TurnID})
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

func allowPendingApprovals(t *testing.T, base string, stop <-chan struct{}) {
	t.Helper()
	for {
		select {
		case <-stop:
			return
		default:
		}
		req, err := http.NewRequest("GET", base+"/approvals", nil)
		if err != nil {
			return
		}
		req.Header.Set("Authorization", "Bearer wsec")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		var list []store.Action
		_ = json.NewDecoder(res.Body).Decode(&list)
		_ = res.Body.Close()
		for _, a := range list {
			if a.ID == "" {
				continue
			}
			preq, _ := http.NewRequest("POST", base+"/approvals/"+a.ID, strings.NewReader(`{"decision":"allow"}`))
			preq.Header.Set("Authorization", "Bearer wsec")
			preq.Header.Set("Content-Type", "application/json")
			pres, err := http.DefaultClient.Do(preq)
			if err == nil {
				_ = pres.Body.Close()
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
}

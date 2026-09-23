package server

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/runner"
	"github.com/sannrox/rusui/internal/store"
)

const agentToken = "github_pat_agent_test"

func agentCredentialEnv(t *testing.T, implement bool, token string) (*httptest.Server, *engine.Engine) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	pol := strings.Replace(slackPol, "implement: false", "implement: "+strconv.FormatBool(implement), 1)
	p, err := policy.Parse([]byte(pol))
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(st, p, gh.NewFake(), &clock.Fake{T: time.Unix(1_700_000_000, 0).UTC()})
	e.Env = env.Process{Root: t.TempDir()}
	e.ReloadPolicy(p)
	hs := httptest.NewServer((&Server{Eng: e, WorkerSec: "wsec", AgentGitHubToken: token}).Handler())
	t.Cleanup(hs.Close)
	return hs, e
}

func implementTask() engine.TaskSpec {
	return engine.TaskSpec{
		EffortKey: "effort-1", Prompt: "open a pull request", Repo: "example/test-repo",
		Ref: "main", BaseSHA: "aaa", AllowedPaths: []string{"docs"},
	}
}

// claim starts a session (an implementation task when task is true, else an
// ordinary run) and claims its turn through the real runner client.
func claim(t *testing.T, hs *httptest.Server, e *engine.Engine, task bool) *runner.Assignment {
	t.Helper()
	if task {
		if _, err := e.StartTask("test", implementTask()); err != nil {
			t.Fatal(err)
		}
	} else if _, err := e.StartRun("test", "open a pull request", ""); err != nil {
		t.Fatal(err)
	}
	c := &runner.Client{Base: hs.URL, Bootstrap: "wsec", Repo: "example/test-repo"}
	a, err := c.Claim()
	if err != nil {
		t.Fatal(err)
	}
	if a == nil {
		t.Fatal("no turn to claim")
	}
	return a
}

func gitCredential(t *testing.T, guestEnv []string) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	cmd := exec.Command(git, "credential", "fill")
	cmd.Env = append(guestEnv, "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1")
	cmd.Stdin = strings.NewReader("protocol=https\nhost=github.com\n\n")
	out, _ := cmd.CombinedOutput()
	return string(out)
}

func TestImplementSessionPushesAsOperator(t *testing.T) {
	hs, e := agentCredentialEnv(t, true, agentToken)
	a := claim(t, hs, e, true)
	if a.GitHubToken != agentToken {
		t.Fatalf("github token %q", a.GitHubToken)
	}
	home := t.TempDir()
	guestEnv := runner.DriverEnv(a, home, os.Getenv("PATH"))
	joined := strings.Join(guestEnv, "\n")
	if !strings.Contains(joined, "GH_TOKEN="+agentToken) {
		t.Fatal("gh has no credential")
	}
	if strings.Contains(joined, "insteadof") || strings.Contains(joined, "Bearer "+a.TurnToken) {
		t.Fatalf("implement session still routed through the git proxy grant:\n%s", joined)
	}
	out := gitCredential(t, guestEnv)
	if !strings.Contains(out, "password="+agentToken) || !strings.Contains(out, "username=x-access-token") {
		t.Fatalf("git credential fill:\n%s", out)
	}
}

func TestSessionsWithoutImplementKeepTheProxyGrant(t *testing.T) {
	for name, tc := range map[string]struct {
		implement bool
		token     string
		task      bool
	}{
		"repo without implement":            {false, agentToken, true},
		"no credential on plane":            {true, "", true},
		"ordinary run on an implement repo": {true, agentToken, false},
	} {
		t.Run(name, func(t *testing.T) {
			hs, e := agentCredentialEnv(t, tc.implement, tc.token)
			a := claim(t, hs, e, tc.task)
			if a.GitHubToken != "" {
				t.Fatalf("github token %q", a.GitHubToken)
			}
			guestEnv := runner.DriverEnv(a, t.TempDir(), os.Getenv("PATH"))
			joined := strings.Join(guestEnv, "\n")
			if strings.Contains(joined, "GH_TOKEN") || !strings.Contains(joined, "insteadof") {
				t.Fatalf("guest env:\n%s", joined)
			}
			if out := gitCredential(t, guestEnv); strings.Contains(out, agentToken) {
				t.Fatalf("credential leaked:\n%s", out)
			}
		})
	}
}

func TestOnlyOpenImplementTasksReceiveTheAgentCredential(t *testing.T) {
	_, e := agentCredentialEnv(t, true, agentToken)
	s := &Server{Eng: e, AgentGitHubToken: agentToken}
	task, err := e.StartTask("test", implementTask())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(e.Store, task.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.agentGitHubToken(sess); got != agentToken {
		t.Fatalf("implement session: %q", got)
	}
	for _, kind := range []string{store.SessionKindReview, store.SessionKindScheduled} {
		other := *sess
		other.Kind = kind
		if got := s.agentGitHubToken(&other); got != "" {
			t.Fatalf("%s session received %q", kind, got)
		}
	}
	unbound := *sess
	unbound.Repo = "other/unbound"
	if got := s.agentGitHubToken(&unbound); got != "" {
		t.Fatalf("unbound repo received %q", got)
	}

	denied := strings.Replace(strings.Replace(slackPol, "implement: false", "implement: true", 1), "session_kinds: [review, run, scheduled]", "session_kinds: [review]", 1)
	p, err := policy.Parse([]byte(denied))
	if err != nil {
		t.Fatal(err)
	}
	e.ReloadPolicy(p)
	if got := s.agentGitHubToken(sess); got != "" {
		t.Fatalf("project no longer admitting run received %q", got)
	}
	allowed := strings.Replace(slackPol, "implement: false", "implement: true", 1)
	if p, err = policy.Parse([]byte(allowed)); err != nil {
		t.Fatal(err)
	}
	e.ReloadPolicy(p)
	if err := e.AbandonEffort(task.EffortKey); err != nil {
		t.Fatal(err)
	}
	if got := s.agentGitHubToken(sess); got != "" {
		t.Fatalf("abandoned task received %q", got)
	}
}

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

func claimRun(t *testing.T, hs *httptest.Server, e *engine.Engine) *runner.Assignment {
	t.Helper()
	if _, err := e.StartRun("test", "open a pull request", ""); err != nil {
		t.Fatal(err)
	}
	c := &runner.Client{Base: hs.URL, Bootstrap: "wsec", Repo: "example/test-repo"}
	a, err := c.Claim()
	if err != nil {
		t.Fatal(err)
	}
	if a == nil {
		t.Fatal("no run turn to claim")
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

func TestImplementRunSessionPushesAsOperator(t *testing.T) {
	hs, e := agentCredentialEnv(t, true, agentToken)
	a := claimRun(t, hs, e)
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
	}{
		"repo without implement": {false, agentToken},
		"no credential on plane": {true, ""},
	} {
		t.Run(name, func(t *testing.T) {
			hs, e := agentCredentialEnv(t, tc.implement, tc.token)
			a := claimRun(t, hs, e)
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

func TestReviewSessionsNeverReceiveTheAgentCredential(t *testing.T) {
	_, e := agentCredentialEnv(t, true, agentToken)
	s := &Server{Eng: e, AgentGitHubToken: agentToken}
	for _, kind := range []string{store.SessionKindRun, store.SessionKindScheduled} {
		if got := s.agentGitHubToken(&store.Session{Kind: kind, Repo: "example/test-repo"}); got != agentToken {
			t.Fatalf("%s session: %q", kind, got)
		}
	}
	if got := s.agentGitHubToken(&store.Session{Kind: store.SessionKindReview, Repo: "example/test-repo"}); got != "" {
		t.Fatalf("review session received %q", got)
	}
	if got := s.agentGitHubToken(&store.Session{Kind: store.SessionKindRun, Repo: "other/unbound"}); got != "" {
		t.Fatalf("unbound repo received %q", got)
	}
}

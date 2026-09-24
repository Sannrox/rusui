package runner_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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

// runTurn starts a run session and hosts one turn through the real plane
// and runner. agent plays the guest in its workspace before the prompt.
func runTurn(t *testing.T, f *gh.Fake, agent func(a *runner.Assignment, dir string)) (st *store.Store, jobID int64) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	pol, err := policy.Parse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(st, pol, f, clock.Real{})
	e.ReloadPolicy(pol)
	e.Env = env.Process{Root: t.TempDir()}
	hs := httptest.NewServer((&server.Server{Eng: e, WorkerSec: "wsec"}).Handler())
	t.Cleanup(hs.Close)
	if _, err := e.StartRun("test", "open a pull request", ""); err != nil {
		t.Fatal(err)
	}
	cli := &runner.Client{Base: hs.URL, Bootstrap: "wsec", Repo: "example/test-repo", Name: "local"}
	err = runner.OneACPTurn(context.Background(), cli, func(a *runner.Assignment, dir string) (*acp.Client, func(), error) {
		if !strings.Contains(strings.Join(runner.DriverEnv(a, dir, ""), "\n"), "RUSUI_RESULT="+a.ResultPath) {
			t.Fatal("guest has no result path")
		}
		agent(a, dir)
		return startFakeACP(t, &runner.HTTPRecorder{Base: hs.URL, Token: a.TurnToken, TurnID: a.TurnID})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRow(`SELECT id FROM turns`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	return st, jobID
}

func turnState(t *testing.T, st *store.Store, jobID int64) string {
	t.Helper()
	var s string
	if err := st.DB.QueryRow(`SELECT state FROM turns WHERE id=?`, jobID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func turnResult(t *testing.T, st *store.Store, jobID int64) engine.TaskResult {
	t.Helper()
	p, err := store.LatestReviewJSON(st, jobID)
	if err != nil {
		t.Fatal(err)
	}
	var art engine.Artifact
	if err := json.Unmarshal([]byte(p), &art); err != nil || art.Result == nil {
		t.Fatalf("payload %s %v", p, err)
	}
	return *art.Result
}

// commitIn makes a commit in dir, as the agent would, and returns its SHA.
func commitIn(t *testing.T, dir string) string {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.name=a", "-c", "user.email=a@example.invalid", "commit", "-q", "--allow-empty", "-m", "change"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func writeResult(t *testing.T, a *runner.Assignment, body string) {
	t.Helper()
	if err := os.WriteFile(a.ResultPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func pullRequest(n int, head string) snapshot.Item {
	return snapshot.Item{Repo: "example/test-repo", Item: n, ItemKind: "pull", State: "open", HeadSHA: head, MainSHA: "aaa"}
}

func TestImplementTurnRecordsPublishedPullRequest(t *testing.T) {
	f := gh.NewFake()
	st, job := runTurn(t, f, func(a *runner.Assignment, dir string) {
		f.Put(pullRequest(7, commitIn(t, dir)))
		writeResult(t, a, `{"pull_request": 7}`)
	})
	if s := turnState(t, st, job); s != "completed" {
		t.Fatalf("turn %s", s)
	}
	r := turnResult(t, st, job)
	if r.Outcome != engine.OutcomePublished || r.PullRequest != 7 || r.CandidateSHA == "" || r.PublishedSHA != r.CandidateSHA {
		t.Fatalf("result %+v", r)
	}
}

func TestClaimedPullRequestNotOnGitHubIsUnconfirmed(t *testing.T) {
	f := gh.NewFake()
	st, job := runTurn(t, f, func(a *runner.Assignment, dir string) {
		commitIn(t, dir)
		f.Put(pullRequest(7, "0000000000000000000000000000000000000000"))
		writeResult(t, a, `{"pull_request": 7}`)
	})
	if r := turnResult(t, st, job); r.Outcome != engine.OutcomeUnconfirmed {
		t.Fatalf("result %+v", r)
	}
}

func TestBlockedTurnNamesItsReason(t *testing.T) {
	st, job := runTurn(t, gh.NewFake(), func(a *runner.Assignment, dir string) {
		writeResult(t, a, `{"blocked_reason": "tests need a database"}`)
	})
	if s := turnState(t, st, job); s != "completed" {
		t.Fatalf("turn %s", s)
	}
	if r := turnResult(t, st, job); r.Outcome != engine.OutcomeBlocked || r.BlockedReason != "tests need a database" {
		t.Fatalf("result %+v", r)
	}
}

func TestRunTurnWithoutResultStillFailsClosed(t *testing.T) {
	st, job := runTurn(t, gh.NewFake(), func(a *runner.Assignment, dir string) {
		writeResult(t, a, "I opened https://github.com/example/test-repo/pull/7")
	})
	if s := turnState(t, st, job); s == "completed" {
		t.Fatal("prose result completed the turn")
	}
	if _, err := store.LatestReviewJSON(st, job); err == nil {
		t.Fatal("prose result was recorded")
	}
}

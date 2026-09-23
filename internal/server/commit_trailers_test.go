package server

import (
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/runner"
	"github.com/sannrox/rusui/internal/store"
)

func gitIn(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(env, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=op", "GIT_AUTHOR_EMAIL=op@example.invalid",
		"GIT_COMMITTER_NAME=op", "GIT_COMMITTER_EMAIL=op@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// agentCommit claims a run turn from the plane, installs the runner's
// attribution hooks, and makes real commits with the guest environment.
func agentCommit(t *testing.T, hs *httptest.Server, s *Server) (msg, amended string, repoHookRan bool) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	if _, err := s.Eng.StartRun("test", "commit something", ""); err != nil {
		t.Fatal(err)
	}
	a, err := (&runner.Client{Base: hs.URL, Bootstrap: "wsec", Repo: "example/test-repo"}).Claim()
	if err != nil || a == nil {
		t.Fatalf("claim %v %v", a, err)
	}
	unhook, err := runner.PrepareCommitHooks(nil, a)
	if err != nil {
		t.Fatal(err)
	}
	defer unhook()
	home := t.TempDir()
	guestEnv := runner.DriverEnv(a, home, os.Getenv("PATH"))

	repo := t.TempDir()
	gitIn(t, repo, guestEnv, "init", "-q")
	marker := filepath.Join(t.TempDir(), "repo-hook-ran")
	hook := fmt.Sprintf("#!/bin/sh\ntouch %q\n", marker)
	if err := os.WriteFile(filepath.Join(repo, ".git", "hooks", "pre-commit"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, guestEnv, "add", "f")
	gitIn(t, repo, guestEnv, "commit", "-q", "-m", "agent change")
	msg = gitIn(t, repo, guestEnv, "log", "-1", "--format=%B")
	gitIn(t, repo, guestEnv, "commit", "-q", "--amend", "--no-edit")
	amended = gitIn(t, repo, guestEnv, "log", "-1", "--format=%B")
	_, statErr := os.Stat(marker)
	return msg, amended, statErr == nil
}

func trailerServer(t *testing.T, s func(*Server)) (*httptest.Server, *Server) {
	t.Helper()
	_, e := agentCredentialEnv(t, false, "")
	srv := &Server{Eng: e, WorkerSec: "wsec"}
	s(srv)
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return hs, srv
}

func TestAgentCommitsCarryAttributionTrailers(t *testing.T) {
	hs, srv := trailerServer(t, func(*Server) {})
	msg, amended, repoHookRan := agentCommit(t, hs, srv)
	if !strings.Contains(msg, CoauthorTrailer) || !strings.Contains(msg, "Rusui-Session: ") {
		t.Fatalf("commit message:\n%s", msg)
	}
	if strings.Count(amended, CoauthorTrailer) != 1 || strings.Count(amended, "Rusui-Session: ") != 1 {
		t.Fatalf("amend duplicated trailers:\n%s", amended)
	}
	if !repoHookRan {
		t.Fatal("the repository's own pre-commit hook did not run")
	}
}

func TestTrailerSwitchesDropOnlyTheirTrailer(t *testing.T) {
	hs, srv := trailerServer(t, func(s *Server) { s.NoCoauthorTrailer = true })
	msg, _, _ := agentCommit(t, hs, srv)
	if strings.Contains(msg, "Co-authored-by") || !strings.Contains(msg, "Rusui-Session: ") {
		t.Fatalf("coauthor switch:\n%s", msg)
	}
	hs, srv = trailerServer(t, func(s *Server) { s.NoSessionTrailer = true })
	msg, _, _ = agentCommit(t, hs, srv)
	if !strings.Contains(msg, CoauthorTrailer) || strings.Contains(msg, "Rusui-Session") {
		t.Fatalf("session switch:\n%s", msg)
	}
}

func TestReviewSessionsGetNoTrailers(t *testing.T) {
	s := &Server{}
	if got := s.commitTrailers(&store.Session{ID: 7, Kind: store.SessionKindReview}); got != nil {
		t.Fatalf("review trailers %q", got)
	}
	got := s.commitTrailers(&store.Session{ID: 7, Kind: store.SessionKindScheduled})
	if len(got) != 2 || got[1] != "Rusui-Session: 7" {
		t.Fatalf("scheduled trailers %q", got)
	}
}

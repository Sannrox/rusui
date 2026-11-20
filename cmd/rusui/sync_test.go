package main

import (
	"errors"
	"fmt"
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
	envpkg "github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/server"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

func TestRemoteNamesRepo(t *testing.T) {
	for url, want := range map[string]bool{
		"https://github.com/example/test-repo.git":      true,
		"https://github.com/Example/Test-Repo/":         true,
		"git@github.com:example/test-repo.git":          true,
		"ssh://git@github.com/example/test-repo":        true,
		"/tmp/x/example/test-repo.git":                  true,
		"https://github.com/example/test-repo-fork.git": false,
		"https://github.com/other/test-repo.git":        false,
	} {
		if got := remoteNamesRepo(url, "example/test-repo"); got != want {
			t.Errorf("remoteNamesRepo(%q) = %v", url, got)
		}
	}
}

// TestSyncCLIBlackbox drives the built rusui binary against a real plane
// handler and real git repositories: an implement session that published
// a pull request, a follow-up that moved it, a stale local checkout, an
// ordinary run that never published, a cancelled session, and an unknown
// session.
func TestSyncCLIBlackbox(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.invalid")
	run := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(dir, file, msg string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, file), []byte(msg+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run(dir, "add", file)
		run(dir, "commit", "-q", "-m", msg)
		return run(dir, "rev-parse", "HEAD")
	}

	// GitHub stand-in: a bare repository whose path ends in owner/repo.
	bare := filepath.Join(root, "example", "test-repo.git")
	run(root, "init", "-q", "--bare", "-b", "main", bare)
	upstream := filepath.Join(root, "upstream")
	run(root, "init", "-q", "-b", "main", upstream)
	commit(upstream, "README", "initial")
	run(upstream, "push", "-q", bare, "main")

	// The operator cloned before main advanced: a stale checkout.
	local := filepath.Join(root, "local")
	run(root, "clone", "-q", bare, local)
	localHead := run(local, "rev-parse", "HEAD")

	commit(upstream, "main.txt", "main advanced")
	run(upstream, "push", "-q", bare, "main")
	run(upstream, "switch", "-q", "-c", "rusui/1/change")
	first := commit(upstream, "docs.txt", "agent change")
	run(upstream, "push", "-q", bare, "HEAD:refs/heads/rusui/1/change", "HEAD:refs/pull/7/head")

	st, err := store.Open(filepath.Join(root, "sync.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	pol, err := policy.Parse([]byte(strings.Replace(reviewCLIPolicy, "implement: false", "implement: true", 1)))
	if err != nil {
		t.Fatal(err)
	}
	fake := gh.NewFake()
	fake.Put(snapshot.Item{Repo: "example/test-repo", Item: 7, ItemKind: "pull", State: "open", HeadSHA: first})
	eng := engine.New(st, pol, fake, &clock.Fake{T: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)})
	eng.ReloadPolicy(pol)
	hs := httptest.NewServer((&server.Server{Eng: eng, WorkerSec: "wsec", OperatorTok: "op"}).Handler())
	t.Cleanup(hs.Close)

	complete := func(result engine.TaskResult) {
		t.Helper()
		c, err := eng.Claim("example/test-repo")
		if err != nil || c == nil {
			t.Fatalf("claim %v %v", c, err)
		}
		result.SchemaVersion, result.SourceHash = engine.ResultSchema, c.ItemHash
		a := engine.Artifact{
			SchemaVersion: 1, Repo: c.Job.Repo, Item: c.Job.Item, ItemKind: c.Job.ItemKind,
			ClaimedRevision: c.Job.ClaimedRevision, SnapshotHash: c.ItemHash, MainSHA: c.Snapshot.MainSHA,
			Verdict: "keep", Confidence: "high", Result: &result,
		}
		if _, err := eng.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
			t.Fatal(err)
		}
	}
	task, err := eng.StartTask("test", engine.TaskSpec{
		EffortKey: "effort-sync", Prompt: "change docs", Repo: "example/test-repo",
		Ref: "main", BaseSHA: "aaa", AllowedPaths: []string{"docs.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	implement := strconv.FormatInt(task.SessionID, 10)
	complete(engine.TaskResult{PullRequest: 7, CandidateSHA: first})

	bin := filepath.Join(root, "rusui")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	sync := func(wantCode int, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"sync", "-url", hs.URL, "-token", "op", "-dir", local}, args...)...)
		out, err := cmd.CombinedOutput()
		code := 0
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if code != wantCode {
			t.Fatalf("sync %v exit %d, want %d:\n%s", args, code, wantCode, out)
		}
		return string(out)
	}
	contains := func(out string, wants ...string) {
		t.Helper()
		for _, want := range wants {
			if !strings.Contains(out, want) {
				t.Fatalf("output missing %q:\n%s", want, out)
			}
		}
	}
	turnCount := func() int {
		t.Helper()
		var n int
		if err := st.DB.QueryRow(`SELECT COUNT(*) FROM turns WHERE session_id=?`, task.SessionID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	turnsBefore := turnCount()

	// Stale checkout, first sync: the session commit and the main commit
	// it builds on both land; the working tree and HEAD stay put.
	out := sync(0, implement)
	contains(out, "published example/test-repo#7 at "+first, "refs/rusui/sessions/"+implement+" "+first[:12]+" new",
		"2 commits landed", "agent change", "main advanced")
	if got := run(local, "rev-parse", "refs/rusui/sessions/"+implement); got != first {
		t.Fatalf("session ref %s, want %s", got, first)
	}
	if run(local, "rev-parse", "HEAD") != localHead || run(local, "status", "--porcelain") != "" {
		t.Fatal("sync changed the local HEAD or working tree")
	}
	if cfg := run(local, "config", "--list", "--local"); strings.Contains(cfg, "credential") || strings.Contains(cfg, "extraheader") {
		t.Fatalf("sync wrote a credential into the checkout:\n%s", cfg)
	}
	contains(sync(0, implement), "already up to date")
	if turnCount() != turnsBefore {
		t.Fatal("sync started a turn")
	}

	// A follow-up turn pushes a new head; sync reports only the new commit.
	second := commit(upstream, "docs.txt", "agent follow-up")
	run(upstream, "push", "-q", bare, "HEAD:refs/heads/rusui/1/change", "HEAD:refs/pull/7/head")
	fake.Put(snapshot.Item{Repo: "example/test-repo", Item: 7, ItemKind: "pull", State: "open", HeadSHA: second})
	if _, _, err := eng.PromptFollowUp(task.SessionID, "tighten the docs"); err != nil {
		t.Fatal(err)
	}
	complete(engine.TaskResult{PullRequest: 7, CandidateSHA: second})
	turnsBefore = turnCount()
	out = sync(0, implement)
	contains(out, "at "+second, first[:12]+".."+second[:12], "1 commit landed", "agent follow-up")
	if strings.Contains(out, "main advanced") || strings.Contains(out, "note:") {
		t.Fatalf("follow-up sync repeated earlier commits:\n%s", out)
	}
	if turnCount() != turnsBefore {
		t.Fatal("sync started a turn")
	}

	// A remote that does not name the repository is refused before fetch.
	run(local, "remote", "add", "other", filepath.Join(root, "other", "repo.git"))
	contains(sync(1, "-remote", "other", implement), "does not name example/test-repo")

	// An ordinary run that reported findings never published.
	runID, err := eng.StartRun("test", "look around", "")
	if err != nil {
		t.Fatal(err)
	}
	complete(engine.TaskResult{Findings: []engine.Finding{{Title: "ok", Body: "nothing to change"}}})
	contains(sync(1, strconv.FormatInt(runID, 10)), "has not published a pull request")

	// A cancelled session is an explicit error even though it once
	// published: first a follow-up cancelled before any runner claimed it.
	if _, _, err := eng.PromptFollowUp(task.SessionID, "queued then cancelled"); err != nil {
		t.Fatal(err)
	}
	if err := eng.CancelSession(task.SessionID); err != nil {
		t.Fatal(err)
	}
	contains(sync(1, implement), "session "+implement+" was cancelled")

	// Then a follow-up cancelled while a runner held it.
	if _, _, err := eng.PromptFollowUp(task.SessionID, "one more"); err != nil {
		t.Fatal(err)
	}
	if c, err := eng.Claim("example/test-repo"); err != nil || c == nil {
		t.Fatalf("claim %v %v", c, err)
	}
	if err := eng.CancelSession(task.SessionID); err != nil {
		t.Fatal(err)
	}
	contains(sync(1, implement), "session "+implement+" was cancelled")

	// A follow-up resumes the session; the earlier cancellation no longer
	// blocks syncing what it published.
	if _, _, err := eng.PromptFollowUp(task.SessionID, "resume"); err != nil {
		t.Fatal(err)
	}
	contains(sync(0, implement), "already up to date")

	contains(sync(1, "999999"), "session 999999 not found")
	contains(sync(2, "nope"), "invalid session id")
}

// TestSyncWorkspaceCLIBlackbox applies an environment HEAD plus
// uncommitted files onto a clean local checkout, then proves a second
// run is a no-op and dirty, cancelled, archived, unknown, and unpublished
// workspaces are refused without changing files.
func TestSyncWorkspaceCLIBlackbox(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.invalid")
	run := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(dir, file, msg string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, file), []byte(msg+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run(dir, "add", file)
		run(dir, "commit", "-q", "-m", msg)
		return run(dir, "rev-parse", "HEAD")
	}

	bare := filepath.Join(root, "example", "test-repo.git")
	run(root, "init", "-q", "--bare", "-b", "main", bare)
	upstream := filepath.Join(root, "upstream")
	run(root, "init", "-q", "-b", "main", upstream)
	commit(upstream, "README", "initial")
	run(upstream, "push", "-q", bare, "main")

	local := filepath.Join(root, "local")
	run(root, "clone", "-q", bare, local)

	st, err := store.Open(filepath.Join(root, "sync-workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	pol, err := policy.Parse([]byte(reviewCLIPolicy))
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New(st, pol, gh.NewFake(), &clock.Fake{T: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)})
	eng.Env = envpkg.Process{Root: filepath.Join(root, "envs")}
	eng.ReloadPolicy(pol)
	hs := httptest.NewServer((&server.Server{Eng: eng, WorkerSec: "wsec", OperatorTok: "op"}).Handler())
	t.Cleanup(hs.Close)

	sid, err := eng.StartRun("test", "workspace sync", "")
	if err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(sid, 10)
	sess, err := store.GetSession(st, sid)
	if err != nil {
		t.Fatal(err)
	}
	envRow, err := store.GetEnvironment(st, sess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(root, "envs", "workspace")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	envRow.Handle = ws
	envRow.Driver = envpkg.KindProcess
	envRow.State = store.EnvReady
	if err := store.UpdateEnvironment(st, *envRow); err != nil {
		t.Fatal(err)
	}
	run(ws, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(ws, "README"), []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(ws, "add", "README")
	run(ws, "commit", "-q", "-m", "initial")
	unpublished := commit(ws, "agent.txt", "unpublished work")
	if err := os.WriteFile(filepath.Join(ws, "dirty.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(root, fmt.Sprintf("rusui-%d", os.Getpid()))
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	sync := func(wantCode int, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"sync", "-url", hs.URL, "-token", "op", "-dir", local}, args...)...)
		out, err := cmd.CombinedOutput()
		code := 0
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if code != wantCode {
			t.Fatalf("sync %v exit %d, want %d:\n%s", args, code, wantCode, out)
		}
		return string(out)
	}
	contains := func(out string, wants ...string) {
		t.Helper()
		for _, want := range wants {
			if !strings.Contains(out, want) {
				t.Fatalf("output missing %q:\n%s", want, out)
			}
		}
	}
	turnCount := func() int {
		t.Helper()
		var n int
		if err := st.DB.QueryRow(`SELECT COUNT(*) FROM turns WHERE session_id=?`, sid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	turnsBefore := turnCount()
	marker := filepath.Join(local, "keep-local.txt")
	if err := os.WriteFile(marker, []byte("stay\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(local, "add", "keep-local.txt")
	run(local, "commit", "-q", "-m", "local only")

	out := sync(0, "-workspace", id)
	contains(out, "session "+id+" workspace "+unpublished)
	if run(local, "rev-parse", "HEAD") != unpublished {
		t.Fatalf("HEAD %s, want %s", run(local, "rev-parse", "HEAD"), unpublished)
	}
	if b, err := os.ReadFile(filepath.Join(local, "dirty.txt")); err != nil || string(b) != "uncommitted\n" {
		t.Fatalf("dirty file %q %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(local, "agent.txt")); err != nil || string(b) != "unpublished work\n" {
		t.Fatalf("unpublished file %q %v", b, err)
	}
	if cfg := run(local, "config", "--list", "--local"); strings.Contains(cfg, "credential") || strings.Contains(cfg, "extraheader") {
		t.Fatalf("sync wrote a credential into the checkout:\n%s", cfg)
	}
	if turnCount() != turnsBefore {
		t.Fatal("workspace sync started a turn")
	}

	contains(sync(0, "-workspace", id), "already up to date")
	if run(local, "rev-parse", "HEAD") != unpublished {
		t.Fatal("second run moved HEAD")
	}

	if err := os.WriteFile(filepath.Join(local, "extra.txt"), []byte("nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	beforeExtra := run(local, "status", "--porcelain")
	contains(sync(1, "-workspace", id), "local checkout is dirty")
	if run(local, "status", "--porcelain") != beforeExtra {
		t.Fatal("dirty refusal changed the checkout")
	}
	_ = os.Remove(filepath.Join(local, "extra.txt"))

	contains(sync(1, id), "has not published a pull request")
	if run(local, "rev-parse", "HEAD") != unpublished {
		t.Fatal("published-ref path changed the workspace checkout")
	}

	emptyID, err := eng.StartRun("test", "no git", "")
	if err != nil {
		t.Fatal(err)
	}
	emptySess, err := store.GetSession(st, emptyID)
	if err != nil {
		t.Fatal(err)
	}
	emptyEnv, err := store.GetEnvironment(st, emptySess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	emptyWS := filepath.Join(root, "envs", "empty-ws")
	if err := os.MkdirAll(emptyWS, 0o700); err != nil {
		t.Fatal(err)
	}
	emptyEnv.Handle = emptyWS
	emptyEnv.Driver = envpkg.KindProcess
	emptyEnv.State = store.EnvReady
	if err := store.UpdateEnvironment(st, *emptyEnv); err != nil {
		t.Fatal(err)
	}
	contains(sync(1, "-workspace", strconv.FormatInt(emptyID, 10)), "has no git workspace")

	if err := eng.CancelSession(sid); err != nil {
		t.Fatal(err)
	}
	headBefore := run(local, "rev-parse", "HEAD")
	contains(sync(1, "-workspace", id), "session "+id+" was cancelled")
	if run(local, "rev-parse", "HEAD") != headBefore {
		t.Fatal("cancelled sync changed HEAD")
	}

	sid2, err := eng.StartRun("test", "archive me", "")
	if err != nil {
		t.Fatal(err)
	}
	id2 := strconv.FormatInt(sid2, 10)
	sess2, err := store.GetSession(st, sid2)
	if err != nil {
		t.Fatal(err)
	}
	env2, err := store.GetEnvironment(st, sess2.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	ws2 := filepath.Join(root, "envs", "archive-ws")
	if err := os.MkdirAll(ws2, 0o700); err != nil {
		t.Fatal(err)
	}
	env2.Handle = ws2
	env2.Driver = envpkg.KindProcess
	env2.State = store.EnvReady
	if err := store.UpdateEnvironment(st, *env2); err != nil {
		t.Fatal(err)
	}
	run(ws2, "init", "-q", "-b", "main")
	commit(ws2, "README", "initial")
	if err := eng.ArchiveSession(sid2); err != nil {
		t.Fatal(err)
	}
	contains(sync(1, "-workspace", id2), "session "+id2+" is archived")
	if run(local, "rev-parse", "HEAD") != headBefore {
		t.Fatal("archived sync changed HEAD")
	}

	contains(sync(1, "-workspace", "999999"), "session 999999 not found")
}

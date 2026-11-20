package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

func TestWorkspaceExportProcessGitAndErrors(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	s, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "export", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(e.Store, sid)
	if err != nil {
		t.Fatal(err)
	}
	envRow, err := store.GetEnvironment(e.Store, sess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	p := e.Env.(env.Process)
	ws := filepath.Join(p.Root, "export-ws")
	initExportGit(t, ws)
	envRow.Handle = ws
	envRow.Driver = env.KindProcess
	envRow.State = store.EnvReady
	if err := store.UpdateEnvironment(e.Store, *envRow); err != nil {
		t.Fatal(err)
	}

	code, ct, body := getWorkspaceExport(t, hs, s.OperatorTok, sid)
	if code != http.StatusOK || ct != "application/x-tar" {
		t.Fatalf("export %d %s %s", code, ct, body)
	}
	head, _, files, err := env.ReadWorkspaceExport(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if head == "" || string(files["README"].Body) != "initial\n" {
		t.Fatalf("head %q files %v", head, files)
	}

	code, _, body = getWorkspaceExport(t, hs, "wsec", sid)
	if code != http.StatusUnauthorized {
		t.Fatalf("worker %d %s", code, body)
	}
	code, _, body = getWorkspaceExport(t, hs, s.OperatorTok, 9_000_001)
	if code != http.StatusNotFound || !strings.Contains(string(body), "not found") {
		t.Fatalf("unknown %d %s", code, body)
	}

	empty := filepath.Join(p.Root, "empty")
	if err := os.MkdirAll(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	envRow.Handle = empty
	_ = store.UpdateEnvironment(e.Store, *envRow)
	code, _, body = getWorkspaceExport(t, hs, s.OperatorTok, sid)
	if code != http.StatusConflict || !strings.Contains(string(body), "unpublished") {
		t.Fatalf("unpublished %d %s", code, body)
	}

	envRow.Handle = ws
	_ = store.UpdateEnvironment(e.Store, *envRow)
	if err := e.CancelSession(sid); err != nil {
		t.Fatal(err)
	}
	code, _, body = getWorkspaceExport(t, hs, s.OperatorTok, sid)
	if code != http.StatusConflict || !strings.Contains(string(body), "cancelled") {
		t.Fatalf("cancelled %d %s", code, body)
	}
}

func TestWorkspaceExportArchivedAndSleepingProcess(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	s, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "export-sleep", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(e.Store, sid)
	if err != nil {
		t.Fatal(err)
	}
	envRow, err := store.GetEnvironment(e.Store, sess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	p := e.Env.(env.Process)
	ws := filepath.Join(p.Root, "sleep-ws")
	initExportGit(t, ws)
	envRow.Handle = ws
	envRow.Driver = env.KindProcess
	envRow.State = store.EnvSleeping
	if err := store.UpdateEnvironment(e.Store, *envRow); err != nil {
		t.Fatal(err)
	}

	code, ct, body := getWorkspaceExport(t, hs, s.OperatorTok, sid)
	if code != http.StatusOK || ct != "application/x-tar" {
		t.Fatalf("sleeping process %d %s %s", code, ct, body)
	}
	got, _ := store.GetEnvironment(e.Store, envRow.ID)
	if got.State != store.EnvSleeping {
		t.Fatalf("woke process %s", got.State)
	}

	if err := e.ArchiveSession(sid); err != nil {
		t.Fatal(err)
	}
	code, _, body = getWorkspaceExport(t, hs, s.OperatorTok, sid)
	if code != http.StatusConflict || !strings.Contains(string(body), "archived") {
		t.Fatalf("archived %d %s", code, body)
	}
}

func TestWorkspaceExportWakesOnlyWhenCaptureFails(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	s, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "export-ctr", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(e.Store, sid)
	if err != nil {
		t.Fatal(err)
	}
	envRow, err := store.GetEnvironment(e.Store, sess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	ws := initExportGit(t, filepath.Join(t.TempDir(), "guest"))

	rt := &env.FakeRuntime{}
	plantGitTree(t, rt, "ctr-export", ws)
	e.Container = env.Container{RT: rt, Image: "img"}
	envRow.Handle = "ctr-export"
	envRow.Driver = env.KindContainer
	envRow.State = store.EnvSleeping
	_ = store.UpdateEnvironment(e.Store, *envRow)

	code, _, body := getWorkspaceExport(t, hs, s.OperatorTok, sid)
	if code != http.StatusOK {
		t.Fatalf("stopped capture %d %s", code, body)
	}
	if len(rt.Started) != 0 {
		t.Fatalf("woke when capture worked: %v", rt.Started)
	}

	rt.FailCaptureWhenStopped = true
	_ = rt.Stop("ctr-export")
	envRow, _ = store.GetEnvironment(e.Store, sess.EnvironmentID)
	envRow.State = store.EnvSleeping
	_ = store.UpdateEnvironment(e.Store, *envRow)
	code, _, body = getWorkspaceExport(t, hs, s.OperatorTok, sid)
	if code != http.StatusOK {
		t.Fatalf("wake capture %d %s", code, body)
	}
	if len(rt.Started) == 0 {
		t.Fatal("did not wake when capture failed")
	}
}

func getWorkspaceExport(t *testing.T, hs *httptest.Server, token string, sid int64) (int, string, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, hs.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/workspace/export", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	ct, _, _ := strings.Cut(res.Header.Get("Content-Type"), ";")
	return res.StatusCode, strings.TrimSpace(ct), b
}

func initExportGit(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.invalid")
	runExportGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runExportGit(t, dir, "add", "README")
	runExportGit(t, dir, "commit", "-q", "-m", "initial")
	return dir
}

func runExportGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func plantGitTree(t *testing.T, rt *env.FakeRuntime, id, src string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rt.SetFileContent(id, filepath.ToSlash(rel), data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

func TestWorkspaceListTopLevelPathAndEscape(t *testing.T) {
	t.Parallel()
	s, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "list", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := store.GetSession(e.Store, sid)
	envRow, _ := store.GetEnvironment(e.Store, sess.EnvironmentID)
	p := e.Env.(env.Process)
	ws := filepath.Join(p.Root, "sess")
	if err := os.MkdirAll(filepath.Join(ws, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "note.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "dir", "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	envRow.Handle = ws
	envRow.Driver = env.KindProcess
	envRow.State = store.EnvReady
	_ = store.UpdateEnvironment(e.Store, *envRow)

	code, body := getWorkspace(t, hs, s.OperatorTok, sid, "")
	if code != http.StatusOK {
		t.Fatalf("top %d %s", code, body)
	}
	var view workspaceListView
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(view.Names, "note.txt") || !slices.Contains(view.Names, "dir") {
		t.Fatalf("top names %q", view.Names)
	}
	if slices.Contains(view.Names, "a.txt") {
		t.Fatalf("recursive %q", view.Names)
	}

	code, body = getWorkspace(t, hs, s.OperatorTok, sid, "dir")
	if code != http.StatusOK {
		t.Fatalf("dir %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(view.Names, []string{"a.txt"}) {
		t.Fatalf("dir names %q", view.Names)
	}

	code, body = getWorkspace(t, hs, s.OperatorTok, sid, "../secret")
	if code != http.StatusBadRequest || !strings.Contains(body, "path") {
		t.Fatalf("escape %d %s", code, body)
	}
	code, _ = getWorkspace(t, hs, "wsec", sid, "")
	if code != http.StatusUnauthorized {
		t.Fatalf("worker %d", code)
	}
	got, err := os.ReadFile(filepath.Join(ws, "note.txt"))
	if err != nil || string(got) != "keep" {
		t.Fatalf("wrote %q %v", got, err)
	}
}

func TestWorkspaceListMissingEnvironmentAndReplacedRoot(t *testing.T) {
	t.Parallel()
	s, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "list-missing", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := store.GetSession(e.Store, sid)
	envRow, _ := store.GetEnvironment(e.Store, sess.EnvironmentID)
	envRow.Handle = ""
	envRow.State = store.EnvReady
	_ = store.UpdateEnvironment(e.Store, *envRow)
	code, body := getWorkspace(t, hs, s.OperatorTok, sid, "")
	if code != http.StatusConflict || !strings.Contains(body, "environment missing") {
		t.Fatalf("missing %d %s", code, body)
	}

	rt := &env.FakeRuntime{Replaced: map[string]bool{"ctr": true}}
	e.Container = env.Container{RT: rt, Image: "img"}
	envRow.Handle = "ctr"
	envRow.Driver = env.KindContainer
	envRow.State = store.EnvReady
	_ = store.UpdateEnvironment(e.Store, *envRow)
	code, body = getWorkspace(t, hs, s.OperatorTok, sid, "")
	if code != http.StatusConflict || !strings.Contains(body, "workspace replaced") {
		t.Fatalf("replaced %d %s", code, body)
	}

	envRow.State = store.EnvSleeping
	_ = store.UpdateEnvironment(e.Store, *envRow)
	code, body = getWorkspace(t, hs, s.OperatorTok, sid, "")
	if code != http.StatusConflict || !strings.Contains(body, "not ready") {
		t.Fatalf("sleep %d %s", code, body)
	}
}

func getWorkspace(t *testing.T, hs *httptest.Server, token string, sid int64, path string) (int, string) {
	t.Helper()
	u := hs.URL + "/sessions/" + strconv.FormatInt(sid, 10) + "/workspace"
	if path != "" {
		u += "?path=" + path
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
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
	return res.StatusCode, string(b)
}

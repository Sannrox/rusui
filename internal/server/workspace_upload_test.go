package server

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

func TestWorkspaceUploadHappyPathAndEscape(t *testing.T) {
	s, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "upload", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := store.GetSession(e.Store, sid)
	envRow, _ := store.GetEnvironment(e.Store, sess.EnvironmentID)
	p := e.Env.(env.Process)
	ws := filepath.Join(p.Root, "sess")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	envRow.Handle = ws
	envRow.Driver = env.KindProcess
	envRow.State = store.EnvReady
	_ = store.UpdateEnvironment(e.Store, *envRow)

	code, body := postWorkspace(t, hs, s.OperatorTok, sid, "note.txt", []byte("hello"))
	if code != http.StatusNoContent {
		t.Fatalf("put %d %s", code, body)
	}
	got, err := os.ReadFile(filepath.Join(ws, "note.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("disk %q %v", got, err)
	}

	code, _ = postWorkspace(t, hs, s.OperatorTok, sid, "../secret", []byte("x"))
	if code != http.StatusBadRequest {
		t.Fatalf("escape %d", code)
	}
	code, _ = postWorkspace(t, hs, "wsec", sid, "note.txt", []byte("x"))
	if code != http.StatusUnauthorized {
		t.Fatalf("worker %d", code)
	}
}

func TestWorkspaceUploadSleepingEnvironment(t *testing.T) {
	s, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "sleep-upload", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := store.GetSession(e.Store, sid)
	envRow, _ := store.GetEnvironment(e.Store, sess.EnvironmentID)
	envRow.Handle = t.TempDir()
	envRow.State = store.EnvSleeping
	_ = store.UpdateEnvironment(e.Store, *envRow)
	code, body := postWorkspace(t, hs, s.OperatorTok, sid, "note.txt", []byte("x"))
	if code != http.StatusConflict || !strings.Contains(body, "not ready") {
		t.Fatalf("sleep %d %s", code, body)
	}
}

func TestWorkspaceUploadOversized(t *testing.T) {
	s, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "big-upload", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := store.GetSession(e.Store, sid)
	envRow, _ := store.GetEnvironment(e.Store, sess.EnvironmentID)
	p := e.Env.(env.Process)
	ws := filepath.Join(p.Root, "sess")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	envRow.Handle = ws
	envRow.Driver = env.KindProcess
	envRow.State = store.EnvReady
	_ = store.UpdateEnvironment(e.Store, *envRow)
	code, _ := postWorkspace(t, hs, s.OperatorTok, sid, "big.bin", bytes.Repeat([]byte("a"), env.WorkspaceUploadCap+1))
	if code != http.StatusBadRequest {
		t.Fatalf("oversize %d", code)
	}
}

func postWorkspace(t *testing.T, hs *httptest.Server, token string, sid int64, path string, data []byte) (int, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, hs.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/workspace?path="+path, &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	return res.StatusCode, string(b)
}

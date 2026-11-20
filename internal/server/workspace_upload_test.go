package server

import (
	"bytes"
	"encoding/json"
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// A successful put receipts the destination path and never the file bytes.
// A second put is a second row. A refused put inserts nothing.
func TestWorkspacePutReceiptsPathNotBytes(t *testing.T) {
	t.Parallel()
	s, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "upload-receipt", "")
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

	secret := []byte("workspace-put-secret-bytes-798")
	code, body := postWorkspace(t, hs, s.OperatorTok, sid, "dir/note.txt", secret)
	if code != http.StatusNoContent {
		t.Fatalf("put %d %s", code, body)
	}
	code, body = postWorkspace(t, hs, s.OperatorTok, sid, "dir/note.txt", []byte("second-put-bytes"))
	if code != http.StatusNoContent {
		t.Fatalf("second put %d %s", code, body)
	}
	code, _ = postWorkspace(t, hs, s.OperatorTok, sid, "../secret", []byte("escaped-bytes"))
	if code != http.StatusBadRequest {
		t.Fatalf("escape %d", code)
	}

	puts := sessionActionsOfType(t, hs, sid, actionWorkspacePut)
	if len(puts) != 2 {
		t.Fatalf("put rows %d: %+v", len(puts), puts)
	}
	for _, a := range puts {
		if a.SessionID == nil || *a.SessionID != sid {
			t.Fatalf("session %+v", a)
		}
		var payload struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(a.Body), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Path != "dir/note.txt" {
			t.Fatalf("path %q", payload.Path)
		}
		if strings.Contains(a.Body, string(secret)) || strings.Contains(a.Body, "second-put-bytes") || strings.Contains(a.Body, "escaped-bytes") {
			t.Fatalf("file bytes in body %q", a.Body)
		}
	}
}

func sessionActionsOfType(t *testing.T, hs *httptest.Server, sid int64, typ string) []store.Action {
	t.Helper()
	raw := getJSON(t, hs.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/logs", "op-tok")
	var acts []store.Action
	if err := json.Unmarshal([]byte(raw), &acts); err != nil {
		t.Fatal(err)
	}
	out := make([]store.Action, 0)
	for _, a := range acts {
		if a.Type == typ {
			out = append(out, a)
		}
	}
	return out
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

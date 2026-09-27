package server

import (
	"database/sql"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/store"
)

func TestSessionReadMatchesConsoleTranscriptAndDiff(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sid := createRunSession(t, hs, "first prompt")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pointWorkspace(t, e, sid, dir)
	toolBody := `{"sessionUpdate":"tool_call","title":"shell"}`
	if err := store.InsertAction(e.Store, store.Action{
		ID: "tool-1", SessionID: &sid, Repo: "example/test-repo", Item: 1,
		Type: acp.ActionUpdate, ReasonCode: acp.ReasonRecorded,
		EvidenceClass: acp.EvidenceObserved, LimitSentence: acp.LimitSentence,
		Body: toolBody,
	}); err != nil {
		t.Fatal(err)
	}

	read := authedGet(t, hs, "/sessions/"+strconv.FormatInt(sid, 10)+"/read", "op-tok")
	if read.code != http.StatusOK {
		t.Fatalf("read %d %s", read.code, read.body)
	}
	var view sessionRead
	if err := json.Unmarshal([]byte(read.body), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Transcript) != 1 || view.Transcript[0].Kind != acp.ActionUpdate || view.Transcript[0].Body != toolBody {
		t.Fatalf("transcript %+v", view.Transcript)
	}
	console := consoleGet(t, hs, "/console/sessions/"+strconv.FormatInt(sid, 10))
	if !strings.Contains(console, acp.ActionUpdate) || !strings.Contains(console, "shell") || !strings.Contains(console, "first prompt") {
		t.Fatalf("console transcript %s", console)
	}
	diffPage := html.UnescapeString(consoleGet(t, hs, "/console/sessions/"+strconv.FormatInt(sid, 10)+"/files?path=note.txt&view=diff"))
	const patch = "+++ b/note.txt"
	if !strings.Contains(diffPage, patch) || !strings.Contains(diffPage, "+hello") {
		t.Fatalf("console diff %s", diffPage)
	}
	if !strings.Contains(view.Diff, patch) || !strings.Contains(view.Diff, "+hello") {
		t.Fatalf("read diff %s", view.Diff)
	}

	again := authedGet(t, hs, "/sessions/"+strconv.FormatInt(sid, 10)+"/read", "op-tok")
	if again.body != read.body {
		t.Fatalf("second read changed\n%s\n%s", read.body, again.body)
	}
}

func TestSessionReadExplicitErrors(t *testing.T) {
	_, hs, e := consoleEnv(t)
	missing := authedGet(t, hs, "/sessions/999999/read", "op-tok")
	if missing.code != http.StatusNotFound || !strings.Contains(missing.body, "not found") {
		t.Fatalf("unknown %+v", missing)
	}
	anon, err := http.Get(hs.URL + "/sessions/1/read")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(anon.Body)
	_ = anon.Body.Close()
	if anon.StatusCode != http.StatusUnauthorized || !strings.Contains(string(b), "auth") {
		t.Fatalf("auth %d %s", anon.StatusCode, b)
	}
	var localID int64
	if err := e.Store.Tx(func(tx *sql.Tx) error {
		var err error
		localID, err = store.InsertLocalSessionTx(tx, "test", time.Now().UTC())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	local := authedGet(t, hs, "/sessions/"+strconv.FormatInt(localID, 10)+"/read", "op-tok")
	if local.code != http.StatusConflict || !strings.Contains(local.body, "no durable transcript") {
		t.Fatalf("local %+v", local)
	}
	worker := authedGet(t, hs, "/sessions/"+strconv.FormatInt(localID, 10)+"/read", "wsec")
	if worker.code != http.StatusUnauthorized {
		t.Fatalf("worker read %d %s", worker.code, worker.body)
	}
}

func TestSessionReadDoesNotFollowWorkspaceSymlink(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sid := createRunSession(t, hs, "first prompt")
	dir := t.TempDir()
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("host-secret-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "note.txt")); err != nil {
		t.Fatal(err)
	}
	pointWorkspace(t, e, sid, dir)
	read := authedGet(t, hs, "/sessions/"+strconv.FormatInt(sid, 10)+"/read", "op-tok")
	if read.code != http.StatusOK {
		t.Fatalf("read %d %s", read.code, read.body)
	}
	if strings.Contains(read.body, "host-secret-value") {
		t.Fatalf("symlink target leaked %s", read.body)
	}
	if !strings.Contains(read.body, "not a regular file") {
		t.Fatalf("symlink state %s", read.body)
	}
}

func createRunSession(t *testing.T, hs *httptest.Server, prompt string) int64 {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, hs.URL+"/projects/test/sessions", strings.NewReader(`{"kind":"run","prompt":`+strconv.Quote(prompt)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, b)
	}
	var created struct {
		SessionID int64 `json:"session_id"`
	}
	if err := json.Unmarshal(b, &created); err != nil || created.SessionID == 0 {
		t.Fatalf("created %s %v", b, err)
	}
	return created.SessionID
}

func pointWorkspace(t *testing.T, e *engine.Engine, sid int64, dir string) {
	t.Helper()
	sess, err := store.GetSession(e.Store, sid)
	if err != nil {
		t.Fatal(err)
	}
	envRow, err := store.GetEnvironment(e.Store, sess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	envRow.Handle = dir
	envRow.State = store.EnvReady
	if envRow.Driver == "" {
		envRow.Driver = "process"
	}
	if err := store.UpdateEnvironment(e.Store, *envRow); err != nil {
		t.Fatal(err)
	}
}

type httpBody struct {
	code int
	body string
}

func authedGet(t *testing.T, hs *httptest.Server, path, token string) httpBody {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, hs.URL+path, nil)
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
	return httpBody{code: res.StatusCode, body: string(b)}
}

func consoleGet(t *testing.T, hs *httptest.Server, path string) string {
	t.Helper()
	res, err := operatorClient(t, hs).Get(hs.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("console %s %d %s", path, res.StatusCode, b)
	}
	return string(b)
}

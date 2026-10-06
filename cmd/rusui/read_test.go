package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/server"
	"github.com/sannrox/rusui/internal/store"
)

func TestFormatSessionReadEscapesTerminalControls(t *testing.T) {
	out := formatSessionRead(sessionReadView{
		SessionID: 1, Kind: "run", Prompt: "ok",
		Diff: "hello\n\x1b]52;c;eA==\a",
	})
	if strings.Contains(out, "\x1b") || strings.Contains(out, "\a") {
		t.Fatalf("control sequence reached the terminal: %q", out)
	}
	if !strings.Contains(out, "hello\n") || !strings.Contains(out, `\u001b`) {
		t.Fatalf("escaped diff %q", out)
	}
}

func TestReadCLIMatchesPlaneAndDoesNotAttach(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	pol, err := policy.Parse([]byte(attachPolicy))
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(st, pol, gh.NewFake(), clock.Real{})
	e.Env = env.Process{Root: t.TempDir()}
	e.ReloadPolicy(pol)
	srv := &server.Server{Eng: e, WorkerSec: "wsec", OperatorTok: "op-tok"}
	var paths []string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		srv.Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(hs.Close)

	req, err := http.NewRequest(http.MethodPost, hs.URL+"/projects/test/sessions", strings.NewReader(`{"kind":"run","prompt":"first prompt"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, raw)
	}
	var created struct {
		SessionID int64 `json:"session_id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.SessionID == 0 {
		t.Fatalf("created %s", raw)
	}
	sid := created.SessionID
	id := strconv.FormatInt(sid, 10)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello\n\x1b[2J"), 0o600); err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(st, sid)
	if err != nil {
		t.Fatal(err)
	}
	envRow, err := store.GetEnvironment(st, sess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	envRow.Handle = dir
	envRow.State = store.EnvReady
	if err := store.UpdateEnvironment(st, *envRow); err != nil {
		t.Fatal(err)
	}
	toolBody := `{"sessionUpdate":"tool_call","title":"shell"}`
	if err := store.InsertAction(st, store.Action{
		ID: "tool-1", SessionID: &sid, Repo: "example/test-repo", Item: 1,
		Type: acp.ActionUpdate, ReasonCode: acp.ReasonRecorded,
		EvidenceClass: acp.EvidenceObserved, LimitSentence: acp.LimitSentence,
		Body: toolBody,
	}); err != nil {
		t.Fatal(err)
	}

	paths = nil
	var out, errOut bytes.Buffer
	if code := readMain([]string{"-url", hs.URL, "-token", "op-tok", id}, &out, &errOut); code != 0 {
		t.Fatalf("read %d %s", code, errOut.String())
	}
	first := out.String()
	if !strings.Contains(first, toolBody) || !strings.Contains(first, "+++ b/note.txt") || !strings.Contains(first, "prompt: first prompt") {
		t.Fatalf("cli %s", first)
	}
	if strings.Contains(first, "\x1b") {
		t.Fatalf("workspace control sequence was printed: %q", first)
	}
	for _, path := range paths {
		if strings.Contains(path, "attach") || strings.Contains(path, "terminal") {
			t.Fatalf("read called %s", path)
		}
	}
	if len(paths) != 1 || paths[0] != "/sessions/"+id+"/read" {
		t.Fatalf("paths %v", paths)
	}

	out.Reset()
	if code := readMain([]string{"-url", hs.URL, "-token", "op-tok", id}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	if out.String() != first {
		t.Fatalf("second read\n%s\n%s", first, out.String())
	}

	follow, err := newPromptRequest(hs.URL, "op-tok", id, "narrow the diff", false, false)
	if err != nil {
		t.Fatal(err)
	}
	res, err = http.DefaultClient.Do(follow)
	if err != nil {
		t.Fatal(err)
	}
	fb, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode >= 300 {
		t.Fatalf("prompt %d %s", res.StatusCode, fb)
	}
	out.Reset()
	if code := readMain([]string{"-url", hs.URL, "-token", "op-tok", id}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	later := out.String()
	if !strings.Contains(later, "prompt: narrow the diff") || !strings.Contains(later, toolBody) {
		t.Fatalf("follow-up read %s", later)
	}

	errOut.Reset()
	if code := readMain([]string{"-url", hs.URL, "-token", "nope", id}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "auth") {
		t.Fatalf("auth code %d %s", code, errOut.String())
	}
	errOut.Reset()
	if code := readMain([]string{"-url", hs.URL, "-token", "op-tok", "999999"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "not found") {
		t.Fatalf("missing code %d %s", code, errOut.String())
	}
}

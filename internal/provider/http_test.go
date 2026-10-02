package provider

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/server"
	"github.com/sannrox/rusui/internal/store"
)

func TestBlackBoxReadShowsGuestTranscriptOnce(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	pol, err := policy.Parse([]byte(blackBoxPolicy))
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(st, pol, gh.NewFake(), clock.Real{})
	e.Env = env.Process{Root: t.TempDir()}
	e.ReloadPolicy(pol)
	hs := httptest.NewServer((&server.Server{Eng: e, WorkerSec: "wsec", OperatorTok: "op-tok"}).Handler())
	t.Cleanup(hs.Close)

	req, err := http.NewRequest(http.MethodPost, hs.URL+"/projects/test/sessions", strings.NewReader(`{"kind":"run","prompt":"start"}`))
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
	detail := getJSON(t, hs, "/sessions/"+strconv.FormatInt(created.SessionID, 10), "wsec")
	var body struct {
		Turns []struct {
			ID int64 `json:"ID"`
		} `json:"turns"`
	}
	if err := json.Unmarshal(detail, &body); err != nil || len(body.Turns) == 0 {
		t.Fatalf("session %s", detail)
	}
	turnID := body.Turns[0].ID
	// Actions are recorded only for a running turn (#437), as a runner
	// posts them after it claims the turn.
	if c, err := e.Claim("example/test-repo"); err != nil || c == nil || c.Job.ID != turnID {
		t.Fatalf("claim %+v %v", c, err)
	}

	cmd := fakeCommand(t, KindGrok, false)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	result, err := Run(t.Context(), KindGrok, Instance{Kind: KindGrok, ID: "box", Dir: t.TempDir()}, stdio{Reader: stdout, WriteCloser: stdin}, Turn{Prompt: "start", Workspace: t.TempDir()}, func([]Option, json.RawMessage) (string, bool) {
		return "allow-once", true
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range result.Events {
		postAction(t, hs, turnID, ev)
	}
	read := string(getJSON(t, hs, "/sessions/"+strconv.FormatInt(created.SessionID, 10)+"/read", "op-tok"))
	if !strings.Contains(read, "hello-grok") || strings.Count(read, "hello-grok") != 1 {
		t.Fatalf("read %s", read)
	}
	again := string(getJSON(t, hs, "/sessions/"+strconv.FormatInt(created.SessionID, 10)+"/read", "op-tok"))
	if again != read {
		t.Fatal("second read changed")
	}
}

func postAction(t *testing.T, hs *httptest.Server, turnID int64, ev Event) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"type": "provider." + ev.Kind, "reason": "recorded", "body": ev.Body})
	req, err := http.NewRequest(http.MethodPost, hs.URL+"/turns/"+strconv.FormatInt(turnID, 10)+"/actions", bytes.NewReader(payload))
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
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("action %d %s", res.StatusCode, b)
	}
}

func getJSON(t *testing.T, hs *httptest.Server, path, token string) []byte {
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
	if res.StatusCode != http.StatusOK {
		t.Fatalf("get %s %d %s", path, res.StatusCode, b)
	}
	return b
}

const blackBoxPolicy = `version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
  review: true
  comments: false
  close: false
  implement: false
  land: false
  max_reviews_per_repo_per_utc_day: 50
projects:
  test:
    repos:
      example/test-repo:
        visibility: public
        review: true
        comments: true
        close: true
        implement: false
        land: false
`

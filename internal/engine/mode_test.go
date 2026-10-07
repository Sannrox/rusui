package engine_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/store"
)

func TestStartRunModeIsStoredAndClaimed(t *testing.T) {
	h := setup(t)
	id, err := h.e.StartRunMode("test", "look around", "", "", engine.SessionModeHigh)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, id)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Mode != engine.SessionModeHigh {
		t.Fatalf("session mode %q", sess.Mode)
	}
	req, _ := http.NewRequest("POST", h.http.URL+"/jobs/claim", strings.NewReader(`{"repo":"example/test-repo"}`))
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out["mode"] != engine.SessionModeHigh {
		t.Fatalf("claim mode %v", out["mode"])
	}
}

func TestStartRunOmitsModeByDefault(t *testing.T) {
	h := setup(t)
	id, err := h.e.StartRun("test", "look around", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, id)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Mode != "" {
		t.Fatalf("session mode %q", sess.Mode)
	}
}

func TestStartRunModeRefusesUnknownName(t *testing.T) {
	h := setup(t)
	if _, err := h.e.StartRunMode("test", "look around", "", "", "turbo"); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestCreateSessionHTTPForwardsMode(t *testing.T) {
	h := setup(t)
	req, _ := http.NewRequest("POST", h.http.URL+"/projects/test/sessions", strings.NewReader(`{"kind":"run","prompt":"look around","mode":"low"}`))
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var out struct {
		SessionID int64 `json:"session_id"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, out.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Mode != engine.SessionModeLow {
		t.Fatalf("session mode %q", sess.Mode)
	}
}

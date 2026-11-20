package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewPromptRequestCanSteer(t *testing.T) {
	req, err := newPromptRequest("http://127.0.0.1:8080/", "secret", "42", "use the narrow fix", true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != http.MethodPost || req.URL.Path != "/sessions/42/turns" || req.Header.Get("Authorization") != "Bearer secret" {
		t.Fatalf("request %s %s headers=%v", req.Method, req.URL, req.Header)
	}
	var body struct {
		Prompt string `json:"prompt"`
		Steer  bool   `json:"steer"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Prompt != "use the narrow fix" || !body.Steer {
		t.Fatalf("body %+v", body)
	}
}

func TestNewPromptRequestCanQueue(t *testing.T) {
	req, err := newPromptRequest("http://127.0.0.1:8080", "secret", "42", "after you finish", false, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Prompt string `json:"prompt"`
		Queued bool   `json:"queued"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if req.URL.Path != "/sessions/42/turns" || body.Prompt != "after you finish" || !body.Queued {
		t.Fatalf("request %s body %+v", req.URL, body)
	}
}

func TestPromptMainSteerRefusesFileFlag(t *testing.T) {
	var out, errw bytes.Buffer
	code := promptMain([]string{"-steer", "-file", "shot.png", "42", "stop"}, &out, &errw)
	if code != 2 || !strings.Contains(errw.String(), "attachments are not a steer") {
		t.Fatalf("code %d stderr %q", code, errw.String())
	}
}

func TestPromptMainSendsFileAttachments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(path, []byte("# notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Prompt      string `json:"prompt"`
		Attachments []struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		} `json:"attachments"`
	}
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/sessions/42/turns" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("body %s: %v", raw, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"turn_id":9,"pending_revision":2,"delivery":"follow_up"}`))
	}))
	t.Cleanup(hs.Close)
	var out, errw bytes.Buffer
	code := promptMain([]string{"-url", hs.URL, "-token", "tok", "-file", path, "42", "look"}, &out, &errw)
	if code != 0 {
		t.Fatalf("code %d stderr %q", code, errw.String())
	}
	if got.Prompt != "look" || len(got.Attachments) != 1 || got.Attachments[0].Name != "notes.md" || got.Attachments[0].Content != base64.StdEncoding.EncodeToString([]byte("# notes")) {
		t.Fatalf("payload %+v", got)
	}
}

func TestNewDropQueueRequest(t *testing.T) {
	req, err := newDropQueueRequest("http://127.0.0.1:8080/", "secret", "42")
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != http.MethodDelete || req.URL.Path != "/sessions/42/queued" || req.Header.Get("Authorization") != "Bearer secret" {
		t.Fatalf("request %s %s headers=%v", req.Method, req.URL, req.Header)
	}
}

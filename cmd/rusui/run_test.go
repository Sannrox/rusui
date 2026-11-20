package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunMainPrintsJSONWithoutHandleWhenStderrIsRedirected(t *testing.T) {
	hs := startRunCreateServer(t, `{"session_id":42,"kind":"run"}`)
	t.Setenv("RUSUI_SESSION_URL", "https://example.test/console/sessions/{session}")
	var out, errw bytes.Buffer
	code := runMain([]string{"-url", hs.URL, "-project", "demo", "-token", "tok", "do the thing"}, &out, &errw, false)
	if code != 0 {
		t.Fatalf("code %d stderr %q", code, errw.String())
	}
	var created struct {
		SessionID int64  `json:"session_id"`
		Kind      string `json:"kind"`
	}
	if err := json.Unmarshal(out.Bytes(), &created); err != nil {
		t.Fatalf("stdout %q: %v", out.String(), err)
	}
	if created.SessionID != 42 || created.Kind != "run" {
		t.Fatalf("stdout %+v", created)
	}
	if strings.Contains(errw.String(), "42") || strings.Contains(errw.String(), "read") || strings.Contains(errw.String(), "attach") || strings.Contains(errw.String(), "example.test") {
		t.Fatalf("redirected stderr leaked handle %q", errw.String())
	}
}

func TestRunMainPrintsFollowableHandleOnTerminalStderr(t *testing.T) {
	hs := startRunCreateServer(t, `{"session_id":42,"kind":"run"}`)
	t.Setenv("RUSUI_SESSION_URL", "https://example.test/console/sessions/{session}")
	var out, errw bytes.Buffer
	code := runMain([]string{"-url", hs.URL, "-project", "demo", "-token", "tok", "do the thing"}, &out, &errw, true)
	if code != 0 {
		t.Fatalf("code %d stderr %q", code, errw.String())
	}
	var created struct {
		SessionID int64 `json:"session_id"`
	}
	if err := json.Unmarshal(out.Bytes(), &created); err != nil || created.SessionID != 42 {
		t.Fatalf("stdout %q", out.String())
	}
	handle := errw.String()
	for _, want := range []string{
		"session 42",
		"https://example.test/console/sessions/42",
		"rusui read -follow 42",
		"rusui read 42",
		"rusui attach 42",
	} {
		if !strings.Contains(handle, want) {
			t.Fatalf("missing %q in handle %q", want, handle)
		}
	}
}

func startRunCreateServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/projects/demo/sessions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(hs.Close)
	return hs
}

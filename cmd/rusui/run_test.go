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

	"github.com/sannrox/rusui/internal/env"
)

func TestRunMainPrintsJSONWithoutHandleWhenStderrIsRedirected(t *testing.T) {
	hs := startRunCreateServer(t, `{"session_id":42,"kind":"run"}`)
	t.Setenv("RUSUI_SESSION_URL", "https://example.test/console/sessions/{session}")
	var out, errw bytes.Buffer
	code := runMain([]string{"-url", hs.URL, "-project", "demo", "-token", "tok", "do the thing"}, strings.NewReader(""), &out, &errw, true, false)
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

func TestRunMainPrintsSnapshotHitOrMissOnTerminalStderr(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"hit", `{"session_id":42,"kind":"run","snapshot_hit":true,"source_hash":"abc"}`, "session 42 snapshot hit"},
		{"miss", `{"session_id":42,"kind":"run","snapshot_hit":false,"source_hash":"abc"}`, "session 42 snapshot miss"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hs := startRunCreateServer(t, tc.body)
			var out, errw bytes.Buffer
			code := runMain([]string{"-url", hs.URL, "-project", "demo", "-token", "tok", "do the thing"}, strings.NewReader(""), &out, &errw, true, true)
			if code != 0 {
				t.Fatalf("code %d stderr %q", code, errw.String())
			}
			var created struct {
				SessionID int64 `json:"session_id"`
			}
			if err := json.Unmarshal(out.Bytes(), &created); err != nil || created.SessionID != 42 {
				t.Fatalf("stdout %q", out.String())
			}
			if !strings.Contains(errw.String(), tc.want) {
				t.Fatalf("stderr %q, want %q", errw.String(), tc.want)
			}
			if !strings.Contains(errw.String(), "rusui read -follow 42") || !strings.Contains(errw.String(), "rusui attach 42") {
				t.Fatalf("handle lines missing %q", errw.String())
			}
		})
	}
}

func TestRunMainPrintsFollowableHandleOnTerminalStderr(t *testing.T) {
	hs := startRunCreateServer(t, `{"session_id":42,"kind":"run"}`)
	t.Setenv("RUSUI_SESSION_URL", "https://example.test/console/sessions/{session}")
	var out, errw bytes.Buffer
	code := runMain([]string{"-url", hs.URL, "-project", "demo", "-token", "tok", "do the thing"}, strings.NewReader(""), &out, &errw, true, true)
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
	return startRunCreateServerFn(t, body, nil)
}

func startRunCreateServerFn(t *testing.T, body string, onCreate func(*http.Request, []byte)) *httptest.Server {
	t.Helper()
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/projects/demo/sessions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		if onCreate != nil {
			onCreate(r, raw)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(hs.Close)
	return hs
}

type failReader struct{ t *testing.T }

func (f failReader) Read([]byte) (int, error) {
	f.t.Fatal("stdin was read")
	return 0, io.EOF
}

func TestRunMainReadsStdinWhenPromptArgsAreEmpty(t *testing.T) {
	var got map[string]any
	hs := startRunCreateServerFn(t, `{"session_id":7,"kind":"run"}`, func(_ *http.Request, raw []byte) {
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("body %s: %v", raw, err)
		}
	})
	var out, errw bytes.Buffer
	code := runMain([]string{"-url", hs.URL, "-project", "demo", "-token", "tok"}, strings.NewReader("  piped prompt\n"), &out, &errw, false, false)
	if code != 0 {
		t.Fatalf("code %d stderr %q", code, errw.String())
	}
	if got["prompt"] != "piped prompt" {
		t.Fatalf("payload %+v", got)
	}
}

func TestRunMainPromptArgsWinOverStdin(t *testing.T) {
	var got map[string]any
	hs := startRunCreateServerFn(t, `{"session_id":7,"kind":"run"}`, func(_ *http.Request, raw []byte) {
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("body %s: %v", raw, err)
		}
	})
	var out, errw bytes.Buffer
	code := runMain([]string{"-url", hs.URL, "-project", "demo", "-token", "tok", "arg prompt"}, strings.NewReader("piped prompt"), &out, &errw, false, false)
	if code != 0 {
		t.Fatalf("code %d stderr %q", code, errw.String())
	}
	if got["prompt"] != "arg prompt" {
		t.Fatalf("payload %+v", got)
	}
}

func TestRunMainEmptyStdinWithoutArgsIsUsage(t *testing.T) {
	var out, errw bytes.Buffer
	code := runMain([]string{"-project", "demo", "-token", "tok"}, strings.NewReader("  \n"), &out, &errw, false, false)
	if code != 2 || !strings.Contains(errw.String(), "usage: rusui run") {
		t.Fatalf("code %d stderr %q", code, errw.String())
	}
}

func TestRunMainTTYWithoutArgsDoesNotReadStdin(t *testing.T) {
	var out, errw bytes.Buffer
	code := runMain([]string{"-project", "demo", "-token", "tok"}, failReader{t}, &out, &errw, true, false)
	if code != 2 || !strings.Contains(errw.String(), "usage: rusui run") {
		t.Fatalf("code %d stderr %q", code, errw.String())
	}
}

func TestRunMainSendsPatchAsBase64(t *testing.T) {
	dir := t.TempDir()
	patchPath := filepath.Join(dir, "local.patch")
	patch := []byte("--- a/README\n+++ b/README\n@@ -1 +1 @@\n-old\n+new\n")
	if err := os.WriteFile(patchPath, patch, 0o600); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	hs := startRunCreateServerFn(t, `{"session_id":11,"kind":"run"}`, func(_ *http.Request, raw []byte) {
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("body %s: %v", raw, err)
		}
	})
	var out, errw bytes.Buffer
	code := runMain([]string{"-url", hs.URL, "-project", "demo", "-token", "tok", "-patch", patchPath, "inspect"}, strings.NewReader(""), &out, &errw, true, false)
	if code != 0 {
		t.Fatalf("code %d stderr %q", code, errw.String())
	}
	if got["prompt"] != "inspect" || got["patch"] != base64.StdEncoding.EncodeToString(patch) {
		t.Fatalf("payload %+v", got)
	}
}

func TestRunMainPatchMissingFileIsError(t *testing.T) {
	var out, errw bytes.Buffer
	code := runMain([]string{"-project", "demo", "-token", "tok", "-patch", filepath.Join(t.TempDir(), "no.patch"), "inspect"}, strings.NewReader(""), &out, &errw, true, false)
	if code != 1 || errw.Len() == 0 {
		t.Fatalf("code %d stderr %q", code, errw.String())
	}
}

func TestRunMainPatchOversizedIsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.patch")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(env.WorkspaceUploadCap + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	var out, errw bytes.Buffer
	code := runMain([]string{"-project", "demo", "-token", "tok", "-patch", path, "inspect"}, strings.NewReader(""), &out, &errw, true, false)
	if code != 1 || !strings.Contains(errw.String(), "oversize") {
		t.Fatalf("code %d stderr %q", code, errw.String())
	}
}

func TestRunMainSendsRepeatableFileAttachments(t *testing.T) {
	dir := t.TempDir()
	notes := filepath.Join(dir, "notes.md")
	shot := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(notes, []byte("# notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	png := []byte{0x89, 0x50, 0x4e, 0x47}
	if err := os.WriteFile(shot, png, 0o600); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Prompt      string `json:"prompt"`
		Attachments []struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		} `json:"attachments"`
	}
	hs := startRunCreateServerFn(t, `{"session_id":9,"kind":"run"}`, func(_ *http.Request, raw []byte) {
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("body %s: %v", raw, err)
		}
	})
	var out, errw bytes.Buffer
	code := runMain([]string{"-url", hs.URL, "-project", "demo", "-token", "tok", "-file", notes, "-file", shot, "inspect"}, strings.NewReader(""), &out, &errw, true, false)
	if code != 0 {
		t.Fatalf("code %d stderr %q", code, errw.String())
	}
	if got.Prompt != "inspect" || len(got.Attachments) != 2 {
		t.Fatalf("payload %+v", got)
	}
	if got.Attachments[0].Name != "notes.md" || got.Attachments[0].Content != base64.StdEncoding.EncodeToString([]byte("# notes")) {
		t.Fatalf("notes %+v", got.Attachments[0])
	}
	if got.Attachments[1].Name != "shot.png" || got.Attachments[1].Content != base64.StdEncoding.EncodeToString(png) {
		t.Fatalf("shot %+v", got.Attachments[1])
	}
}

func TestRunMainFollowPrintsCreateThenFollowsUntilCompleted(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/projects/demo/sessions":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"session_id":42,"kind":"run"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/sessions/42/read/follow":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: session\ndata: {\"session_id\":42,\"kind\":\"run\",\"prompt\":\"do the thing\"}\n\n")
			_, _ = io.WriteString(w, "event: state\ndata: {\"state\":\"completed\"}\n\n")
			_, _ = io.WriteString(w, "event: end\ndata: {\"state\":\"completed\"}\n\n")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(hs.Close)
	var out, errw bytes.Buffer
	code := runMain([]string{"-url", hs.URL, "-project", "demo", "-token", "tok", "-follow", "do the thing"}, strings.NewReader(""), &out, &errw, true, true)
	if code != 0 {
		t.Fatalf("code %d stderr %q", code, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, `"session_id":42`) || !strings.Contains(got, "session 42 run") || !strings.Contains(got, "state: completed") {
		t.Fatalf("stdout %q", got)
	}
	if !strings.Contains(errw.String(), "session 42") || !strings.Contains(errw.String(), "rusui read -follow 42") {
		t.Fatalf("handle %q", errw.String())
	}
}

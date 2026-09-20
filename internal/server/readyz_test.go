package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/ops"
)

func TestReadyzAndBoundedRunSession(t *testing.T) {
	e, _, _ := eventEnv(t)
	dir := t.TempDir()
	pol := filepath.Join(dir, "policy.yaml")
	b, err := os.ReadFile(filepath.Join("..", "..", "policy.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// Bind the fixture project used by eventEnv (test / example/test-repo).
	b = []byte(strings.ReplaceAll(string(b), "Sannrox/rusui", "example/test-repo"))
	if err := os.WriteFile(pol, b, 0o600); err != nil {
		t.Fatal(err)
	}
	cert := filepath.Join(dir, "c")
	key := filepath.Join(dir, "k")
	ca := filepath.Join(dir, "ca")
	for _, p := range []string{cert, key, ca} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	getenv := func(k string) string {
		switch k {
		case "RUSUI_GUEST_IMAGE":
			return "rusui-guest:test"
		case "RUSUI_TLS_CERT":
			return cert
		case "RUSUI_TLS_KEY":
			return key
		case "RUSUI_PLANE_CA":
			return ca
		case "RUSUI_WORKER_SECRET":
			return "wsec"
		default:
			return ""
		}
	}
	srv := &Server{
		Eng: e, WorkerSec: "wsec", PolicyPath: pol, Addr: "127.0.0.1:8080",
		DiagnoseLookRuntime: func() (env.Runtime, error) { return env.DockerCLI{Bin: "docker"}, nil },
		DiagnoseEnv:         getenv,
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	srv.DiagnosePlaneURL = hs.URL
	srv.DiagnoseHTTP = hs.Client()

	res, err := http.Get(hs.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("readyz %d %s", res.StatusCode, body)
	}
	var rep ops.Report
	if err := json.Unmarshal(body, &rep); err != nil || !rep.Ready {
		t.Fatalf("report %s %v", body, err)
	}
	if strings.Contains(string(body), "wsec") {
		t.Fatal("secret leaked")
	}

	req, err := http.NewRequest("POST", hs.URL+"/projects/test/sessions", strings.NewReader(`{"kind":"run","prompt":"hello topology"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("session %d %s", res.StatusCode, body)
	}
	if !strings.Contains(string(body), `"session_id"`) {
		t.Fatalf("session body %s", body)
	}
}

func TestReadyzUnavailableWhenPlaneDown(t *testing.T) {
	e, _, _ := eventEnv(t)
	srv := &Server{
		Eng: e, PolicyPath: filepath.Join(t.TempDir(), "missing.yaml"),
		DiagnoseLookRuntime: func() (env.Runtime, error) { return nil, fmt.Errorf("none") },
		DiagnoseEnv:         func(string) string { return "" },
		DiagnosePlaneURL:    "http://127.0.0.1:1",
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	res, err := http.Get(hs.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("code %d", res.StatusCode)
	}
}

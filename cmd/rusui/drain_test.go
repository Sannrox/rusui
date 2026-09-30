package main

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sannrox/rusui/internal/store"
)

func TestDrainMainPostsWorkerAuth(t *testing.T) {
	var sawAuth string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost || r.URL.Path != "/drain" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"paused":true,"live":[]}`))
	}))
	t.Cleanup(hs.Close)
	var buf bytes.Buffer
	code := drainMain([]string{"-url", hs.URL, "-token", "wsec"}, &buf)
	if code != 0 {
		t.Fatalf("code %d %s", code, buf.String())
	}
	if sawAuth != "Bearer wsec" {
		t.Fatalf("auth %q", sawAuth)
	}
	if buf.Len() == 0 {
		t.Fatal("empty")
	}
}

func TestDrainMainHTTPSUsesPlaneCA(t *testing.T) {
	var sawAuth string
	hs := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"paused":true,"live":[]}`))
	}))
	t.Cleanup(hs.Close)
	ca := t.TempDir() + "/ca.crt"
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: hs.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RUSUI_PLANE_CA", ca)
	var buf bytes.Buffer
	code := drainMain([]string{"-url", hs.URL, "-token", "wsec"}, &buf)
	if code != 0 {
		t.Fatalf("code %d %s", code, buf.String())
	}
	if sawAuth != "Bearer wsec" {
		t.Fatalf("auth %q", sawAuth)
	}
}

func TestDrainMainHTTPSRequiresCA(t *testing.T) {
	t.Setenv("RUSUI_PLANE_CA", "")
	var buf bytes.Buffer
	code := drainMain([]string{"-url", "https://127.0.0.1:1", "-token", "wsec"}, &buf)
	if code == 0 {
		t.Fatal("expected CA required")
	}
}

func TestResumeMainPostsProjectWithWorkerAuth(t *testing.T) {
	var sawAuth, sawURI string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth, sawURI = r.Header.Get("Authorization"), r.Method+" "+r.URL.RequestURI()
		_, _ = w.Write([]byte(`{"paused":false,"project":"rusui"}`))
	}))
	t.Cleanup(hs.Close)
	var buf bytes.Buffer
	if code := resumeMain([]string{"-url", hs.URL, "-token", "wsec", "-project", "rusui"}, &buf); code != 0 {
		t.Fatalf("code %d", code)
	}
	if sawAuth != "Bearer wsec" || sawURI != "POST /resume?project=rusui" || !bytes.Contains(buf.Bytes(), []byte(`"paused":false`)) {
		t.Fatalf("auth %q uri %q out %s", sawAuth, sawURI, buf.String())
	}
}

func TestRestoreMainRestoresACopyAndPrintsInventory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "copy.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if code := restoreMain([]string{"-db", path}, &buf); code != 0 {
		t.Fatalf("code %d", code)
	}
	var rep store.RestoreReport
	if err := json.Unmarshal(buf.Bytes(), &rep); err != nil || rep.Path != path {
		t.Fatalf("report %s %v", buf.String(), err)
	}
	if code := restoreMain(nil, &buf); code != 2 {
		t.Fatalf("missing -db code %d", code)
	}
	if code := restoreMain([]string{"-db", filepath.Join(t.TempDir(), "missing.db")}, &buf); code != 1 {
		t.Fatalf("missing artifact code %d", code)
	}
}

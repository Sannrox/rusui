package main

import (
	"bytes"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
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

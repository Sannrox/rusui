package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
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

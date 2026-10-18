package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHaltMainPostsJSONWorkerAuth(t *testing.T) {
	var sawAuth, sawURI, sawBody string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawURI = r.Method + " " + r.URL.Path
		b, _ := io.ReadAll(r.Body)
		sawBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"halted":true,"scope":"agent","target":"alice","stopped":[]}`))
	}))
	t.Cleanup(hs.Close)
	var buf bytes.Buffer
	code := haltMain([]string{"-url", hs.URL, "-token", "wsec", "-scope", "agent", "-target", "alice", "-reason", "freeze"}, &buf)
	if code != 0 {
		t.Fatalf("code %d %s", code, buf.String())
	}
	if sawAuth != "Bearer wsec" || sawURI != "POST /halt" {
		t.Fatalf("auth %q uri %q", sawAuth, sawURI)
	}
	if !bytes.Contains([]byte(sawBody), []byte(`"scope":"agent"`)) || !bytes.Contains([]byte(sawBody), []byte(`"target":"alice"`)) {
		t.Fatalf("body %s", sawBody)
	}
}

func TestUnhaltMainPostsJSONWorkerAuth(t *testing.T) {
	var sawURI string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawURI = r.Method + " " + r.URL.Path
		_, _ = w.Write([]byte(`{"halted":false,"scope":"agent","target":"alice"}`))
	}))
	t.Cleanup(hs.Close)
	var buf bytes.Buffer
	if code := unhaltMain([]string{"-url", hs.URL, "-token", "wsec", "-scope", "agent", "-target", "alice"}, &buf); code != 0 {
		t.Fatalf("code %d", code)
	}
	if sawURI != "POST /unhalt" {
		t.Fatalf("uri %q", sawURI)
	}
}

func TestHaltMainRequiresScopeAndTarget(t *testing.T) {
	var buf bytes.Buffer
	if code := haltMain([]string{"-scope", "agent"}, &buf); code != 2 {
		t.Fatalf("code %d", code)
	}
}

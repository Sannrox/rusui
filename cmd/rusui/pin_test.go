package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPinMainPostsSession(t *testing.T) {
	var sawAuth, sawURI string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth, sawURI = r.Header.Get("Authorization"), r.Method+" "+r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"session":{"id":7,"pinned":true}}`))
	}))
	t.Cleanup(hs.Close)
	var buf bytes.Buffer
	code := archiveMain("pin", []string{"-url", hs.URL, "-token", "wsec", "7"}, &buf)
	if code != 0 {
		t.Fatalf("code %d %s", code, buf.String())
	}
	if sawAuth != "Bearer wsec" || sawURI != "POST /sessions/7/pin" {
		t.Fatalf("request auth=%q uri=%q", sawAuth, sawURI)
	}
}

func TestUnpinMainPostsSession(t *testing.T) {
	var sawURI string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawURI = r.Method + " " + r.URL.Path
		_, _ = w.Write([]byte(`{"ok":true,"session":{"id":7,"pinned":false}}`))
	}))
	t.Cleanup(hs.Close)
	var buf bytes.Buffer
	code := archiveMain("unpin", []string{"-url", hs.URL, "-token", "wsec", "7"}, &buf)
	if code != 0 {
		t.Fatalf("code %d %s", code, buf.String())
	}
	if sawURI != "POST /sessions/7/unpin" {
		t.Fatalf("uri %q", sawURI)
	}
}

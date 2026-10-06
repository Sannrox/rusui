package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestArchiveMainPostsSession(t *testing.T) {
	var sawAuth, sawURI string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth, sawURI = r.Header.Get("Authorization"), r.Method+" "+r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"session":{"id":7,"archived":true}}`))
	}))
	t.Cleanup(hs.Close)
	var buf bytes.Buffer
	code := archiveMain("archive", []string{"-url", hs.URL, "-token", "wsec", "7"}, &buf)
	if code != 0 {
		t.Fatalf("code %d %s", code, buf.String())
	}
	if sawAuth != "Bearer wsec" || sawURI != "POST /sessions/7/archive" {
		t.Fatalf("request auth=%q uri=%q", sawAuth, sawURI)
	}
}

func TestUnarchiveMainPostsSession(t *testing.T) {
	var sawURI string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawURI = r.Method + " " + r.URL.Path
		_, _ = w.Write([]byte(`{"ok":true,"session":{"id":7,"archived":false}}`))
	}))
	t.Cleanup(hs.Close)
	var buf bytes.Buffer
	code := archiveMain("unarchive", []string{"-url", hs.URL, "-token", "wsec", "7"}, &buf)
	if code != 0 {
		t.Fatalf("code %d %s", code, buf.String())
	}
	if sawURI != "POST /sessions/7/unarchive" {
		t.Fatalf("uri %q", sawURI)
	}
}

func TestNewArchiveRequest(t *testing.T) {
	req, err := newArchiveRequest("http://127.0.0.1:8080/", "secret", "42", "archive")
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != http.MethodPost || req.URL.Path != "/sessions/42/archive" || req.Header.Get("Authorization") != "Bearer secret" {
		t.Fatalf("request %s %s headers=%v", req.Method, req.URL, req.Header)
	}
}

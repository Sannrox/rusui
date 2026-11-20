package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPreviewMainPrintsURL(t *testing.T) {
	t.Parallel()
	var sawAuth, sawURI, sawBody string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		sawAuth, sawURI, sawBody = r.Header.Get("Authorization"), r.Method+" "+r.URL.Path, string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"http://127.0.0.1:8090/?g=previewtok"}`))
	}))
	t.Cleanup(hs.Close)
	var out, errw bytes.Buffer
	code := previewMain([]string{"-url", hs.URL, "-token", "op-tok", "-port", "3000", "7"}, &out, &errw)
	if code != 0 {
		t.Fatalf("code %d %s", code, errw.String())
	}
	if sawAuth != "Bearer op-tok" || sawURI != "POST /sessions/7/preview" {
		t.Fatalf("request auth=%q uri=%q", sawAuth, sawURI)
	}
	var posted struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal([]byte(sawBody), &posted); err != nil || posted.Port != 3000 {
		t.Fatalf("body %q", sawBody)
	}
	if out.String() != "http://127.0.0.1:8090/?g=previewtok\n" {
		t.Fatalf("stdout %q", out.String())
	}
}

func TestPreviewMainUsage(t *testing.T) {
	t.Parallel()
	var out, errw bytes.Buffer
	if code := previewMain(nil, &out, &errw); code != 2 {
		t.Fatalf("code %d %s", code, errw.String())
	}
	if code := previewMain([]string{"-port", "80", "7"}, &out, &errw); code != 2 {
		t.Fatalf("low port %d %s", code, errw.String())
	}
	if code := previewMain([]string{"-port", "3000", "nope"}, &out, &errw); code != 2 {
		t.Fatalf("session %d %s", code, errw.String())
	}
	if code := previewMain([]string{"-service", "web", "-port", "3000", "7"}, &out, &errw); code != 2 {
		t.Fatalf("both flags %d %s", code, errw.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout %q", out.String())
	}
}

func TestPreviewMainNamedService(t *testing.T) {
	t.Parallel()
	var sawBody string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		sawBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"http://127.0.0.1:8090/?g=previewtok"}`))
	}))
	t.Cleanup(hs.Close)
	var out, errw bytes.Buffer
	code := previewMain([]string{"-url", hs.URL, "-token", "op-tok", "-service", "web", "7"}, &out, &errw)
	if code != 0 {
		t.Fatalf("code %d %s", code, errw.String())
	}
	var posted struct {
		Service string `json:"service"`
		Port    int    `json:"port"`
	}
	if err := json.Unmarshal([]byte(sawBody), &posted); err != nil || posted.Service != "web" || posted.Port != 0 {
		t.Fatalf("body %q", sawBody)
	}
	if out.String() != "http://127.0.0.1:8090/?g=previewtok\n" {
		t.Fatalf("stdout %q", out.String())
	}
}

func TestPreviewMainServerError(t *testing.T) {
	t.Parallel()
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "preview origin unset", http.StatusConflict)
	}))
	t.Cleanup(hs.Close)
	var out, errw bytes.Buffer
	code := previewMain([]string{"-url", hs.URL, "-token", "op-tok", "-port", "3000", "7"}, &out, &errw)
	if code != 1 {
		t.Fatalf("code %d %s", code, errw.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout %q", out.String())
	}
	if !strings.Contains(errw.String(), "preview origin unset") {
		t.Fatalf("stderr %q", errw.String())
	}
}

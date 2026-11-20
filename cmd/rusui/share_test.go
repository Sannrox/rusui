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

func TestShareMainPrintsURL(t *testing.T) {
	t.Parallel()
	var sawAuth, sawURI, sawBody string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		sawAuth, sawURI, sawBody = r.Header.Get("Authorization"), r.Method+" "+r.URL.Path, string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"http://127.0.0.1:8090/?g=sharetok"}`))
	}))
	t.Cleanup(hs.Close)
	var out, errw bytes.Buffer
	code := shareMain([]string{"-url", hs.URL, "-token", "op-tok", "-service", "web", "-ttl", "4h", "7"}, &out, &errw)
	if code != 0 {
		t.Fatalf("code %d %s", code, errw.String())
	}
	if sawAuth != "Bearer op-tok" || sawURI != "POST /sessions/7/share" {
		t.Fatalf("request auth=%q uri=%q", sawAuth, sawURI)
	}
	var posted struct {
		Service string `json:"service"`
		TTL     string `json:"ttl"`
		Port    int    `json:"port"`
	}
	if err := json.Unmarshal([]byte(sawBody), &posted); err != nil || posted.Service != "web" || posted.TTL != "4h" || posted.Port != 0 {
		t.Fatalf("body %q", sawBody)
	}
	if out.String() != "http://127.0.0.1:8090/?g=sharetok\n" {
		t.Fatalf("stdout %q", out.String())
	}
}

func TestShareMainListAndRevoke(t *testing.T) {
	t.Parallel()
	var saw string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = r.Method + " " + r.URL.Path
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"token_hash":"abc","service":"web","port":3000}]`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(hs.Close)
	var out, errw bytes.Buffer
	if code := shareMain([]string{"-url", hs.URL, "-token", "op-tok", "-list", "7"}, &out, &errw); code != 0 {
		t.Fatalf("list %d %s", code, errw.String())
	}
	if saw != "GET /sessions/7/share" || !strings.Contains(out.String(), `"token_hash":"abc"`) {
		t.Fatalf("list saw=%q out=%q", saw, out.String())
	}
	out.Reset()
	errw.Reset()
	if code := shareMain([]string{"-url", hs.URL, "-token", "op-tok", "-revoke", "abc", "7"}, &out, &errw); code != 0 {
		t.Fatalf("revoke %d %s", code, errw.String())
	}
	if saw != "DELETE /sessions/7/share/abc" || out.Len() != 0 {
		t.Fatalf("revoke saw=%q out=%q", saw, out.String())
	}
}

func TestShareMainUsage(t *testing.T) {
	t.Parallel()
	var out, errw bytes.Buffer
	if code := shareMain(nil, &out, &errw); code != 2 {
		t.Fatalf("code %d %s", code, errw.String())
	}
	if code := shareMain([]string{"7"}, &out, &errw); code != 2 {
		t.Fatalf("no service %d %s", code, errw.String())
	}
	if code := shareMain([]string{"-service", "web", "nope"}, &out, &errw); code != 2 {
		t.Fatalf("session %d %s", code, errw.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout %q", out.String())
	}
}

func TestShareMainServerError(t *testing.T) {
	t.Parallel()
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "sharing disabled", http.StatusConflict)
	}))
	t.Cleanup(hs.Close)
	var out, errw bytes.Buffer
	code := shareMain([]string{"-url", hs.URL, "-token", "op-tok", "-service", "web", "7"}, &out, &errw)
	if code != 1 {
		t.Fatalf("code %d %s", code, errw.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout %q", out.String())
	}
	if !strings.Contains(errw.String(), "sharing disabled") {
		t.Fatalf("stderr %q", errw.String())
	}
}

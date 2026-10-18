package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReplayMainPostsTurnPath(t *testing.T) {
	var sawAuth, sawURI string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawURI = r.Method + " " + r.URL.Path
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"divergence":"","denied_effects":["bash"]}`))
	}))
	t.Cleanup(hs.Close)
	var buf bytes.Buffer
	code := replayMain([]string{"-url", hs.URL, "-token", "wsec", "12", "34"}, &buf, io.Discard)
	if code != 0 {
		t.Fatalf("code %d %s", code, buf.String())
	}
	if sawAuth != "Bearer wsec" || sawURI != "POST /sessions/12/turns/34/replay" {
		t.Fatalf("auth %q uri %q", sawAuth, sawURI)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"denied_effects"`)) {
		t.Fatalf("out %s", buf.String())
	}
}

func TestReplayMainRequiresIds(t *testing.T) {
	var buf bytes.Buffer
	if code := replayMain(nil, io.Discard, &buf); code != 2 {
		t.Fatalf("code %d", code)
	}
}

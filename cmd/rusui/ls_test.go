package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLsMainPrintsNamesAndPath(t *testing.T) {
	var saw string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = r.Method + " " + r.URL.RequestURI()
		if r.Header.Get("Authorization") != "Bearer op" {
			t.Error("operator token not sent")
			http.Error(w, "auth", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"names":["dir","note.txt"]}`)
	}))
	t.Cleanup(hs.Close)

	var out, errb bytes.Buffer
	if code := lsMain([]string{"-url", hs.URL, "-token", "op", "7"}, &out, &errb); code != 0 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	if saw != "GET /sessions/7/workspace" {
		t.Fatalf("request %q", saw)
	}
	if out.String() != "dir\nnote.txt\n" {
		t.Fatalf("out %q", out.String())
	}

	out.Reset()
	errb.Reset()
	if code := lsMain([]string{"-url", hs.URL, "-token", "op", "7", "dir"}, &out, &errb); code != 0 {
		t.Fatalf("path code %d %s", code, errb.String())
	}
	if saw != "GET /sessions/7/workspace?path=dir" {
		t.Fatalf("path request %q", saw)
	}
}

func TestLsMainFileIsNotADirectory(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte{0x00, 0x01})
	}))
	t.Cleanup(hs.Close)
	var errb bytes.Buffer
	if code := lsMain([]string{"-url", hs.URL, "-token", "op", "7", "note.bin"}, io.Discard, &errb); code != 1 || !strings.Contains(errb.String(), "not a directory") {
		t.Fatalf("file %d %s", code, errb.String())
	}
}

func TestLsMainUsageAndErrors(t *testing.T) {
	var errb bytes.Buffer
	if code := lsMain(nil, io.Discard, &errb); code != 2 || !strings.Contains(errb.String(), "usage:") {
		t.Fatalf("usage %d %s", code, errb.String())
	}
	errb.Reset()
	if code := lsMain([]string{"nope"}, io.Discard, &errb); code != 2 || !strings.Contains(errb.String(), "invalid session id") {
		t.Fatalf("id %d %s", code, errb.String())
	}

	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "environment missing", http.StatusConflict)
	}))
	t.Cleanup(hs.Close)
	errb.Reset()
	if code := lsMain([]string{"-url", hs.URL, "-token", "op", "7"}, io.Discard, &errb); code != 1 || !strings.Contains(errb.String(), "environment missing") {
		t.Fatalf("missing %d %s", code, errb.String())
	}
}

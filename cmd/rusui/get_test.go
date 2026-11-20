package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetMainWritesFile(t *testing.T) {
	bin := []byte{0x00, 0x01, 0xff, 'x'}
	var saw string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = r.Method + " " + r.URL.RequestURI()
		if r.Header.Get("Authorization") != "Bearer op" {
			t.Error("operator token not sent")
			http.Error(w, "auth", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(bin)
	}))
	t.Cleanup(hs.Close)

	dest := filepath.Join(t.TempDir(), fmt.Sprintf("got-%d.bin", os.Getpid()))
	var out, errb bytes.Buffer
	if code := getMain([]string{"-url", hs.URL, "-token", "op", "-file", dest, "7", "note.bin"}, &out, &errb); code != 0 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	if saw != "GET /sessions/7/workspace?path=note.bin" {
		t.Fatalf("request %q", saw)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, bin) {
		t.Fatalf("disk %q %v", got, err)
	}
}

func TestGetMainUsageAndErrors(t *testing.T) {
	var errb bytes.Buffer
	if code := getMain(nil, io.Discard, &errb); code != 2 || !strings.Contains(errb.String(), "usage:") {
		t.Fatalf("usage %d %s", code, errb.String())
	}
	errb.Reset()
	if code := getMain([]string{"7", "note.bin"}, io.Discard, &errb); code != 2 || !strings.Contains(errb.String(), "usage:") {
		t.Fatalf("no file %d %s", code, errb.String())
	}
	errb.Reset()
	if code := getMain([]string{"-file", "x", "nope", "note.bin"}, io.Discard, &errb); code != 2 || !strings.Contains(errb.String(), "invalid session id") {
		t.Fatalf("id %d %s", code, errb.String())
	}

	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"names":["note.bin"]}`)
	}))
	t.Cleanup(hs.Close)
	dest := filepath.Join(t.TempDir(), fmt.Sprintf("dir-%d.bin", os.Getpid()))
	errb.Reset()
	if code := getMain([]string{"-url", hs.URL, "-token", "op", "-file", dest, "7", "dir"}, io.Discard, &errb); code != 1 || !strings.Contains(errb.String(), "not a file") {
		t.Fatalf("dir %d %s", code, errb.String())
	}
	if _, err := os.Stat(dest); err == nil {
		t.Fatal("wrote directory listing")
	}

	hs = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "environment missing", http.StatusConflict)
	}))
	t.Cleanup(hs.Close)
	errb.Reset()
	if code := getMain([]string{"-url", hs.URL, "-token", "op", "-file", dest, "7", "note.bin"}, io.Discard, &errb); code != 1 || !strings.Contains(errb.String(), "environment missing") {
		t.Fatalf("missing %d %s", code, errb.String())
	}
}

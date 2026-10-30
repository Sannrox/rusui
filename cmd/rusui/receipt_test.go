package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/receipt"
)

func TestReceiptMainWritesMarkdownAndFiles(t *testing.T) {
	exp := receipt.Export{
		SchemaVersion: 1, SessionID: 9, TurnID: 3, ReviewRevisionID: 12,
		Verdict: "keep", EvidenceClass: "model_assertion",
		GitHubAction: receipt.GitHubAction{Status: "none", Detail: "No GitHub action occurred."},
		Actions:      []receipt.Action{},
	}
	raw, err := json.Marshal(exp)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sessions/9/receipt" || r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			_, _ = w.Write([]byte("# rusui review receipt\n\nNo GitHub action occurred.\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
	t.Cleanup(hs.Close)

	var out, errw bytes.Buffer
	if code := receiptMain([]string{"-url", hs.URL, "-token", "tok", "9"}, &out, &errw); code != 0 {
		t.Fatalf("md %d %s", code, errw.String())
	}
	if !strings.Contains(out.String(), "No GitHub action occurred.") {
		t.Fatalf("markdown %s", out.String())
	}

	dir := t.TempDir()
	out.Reset()
	errw.Reset()
	if code := receiptMain([]string{"-url", hs.URL, "-token", "tok", "-out", dir, "9"}, &out, &errw); code != 0 {
		t.Fatalf("out %d %s", code, errw.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "r12.json")); err != nil {
		t.Fatal(err)
	}
	md, err := os.ReadFile(filepath.Join(dir, "r12.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "not a recovery input") {
		t.Fatalf("written md %s", md)
	}
}

func TestReceiptMainUsage(t *testing.T) {
	var out, errw bytes.Buffer
	if code := receiptMain(nil, &out, &errw); code != 2 {
		t.Fatalf("code %d %s", code, errw.String())
	}
}

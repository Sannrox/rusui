package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnoseMainExitCodes(t *testing.T) {
	dir := t.TempDir()
	pol := filepath.Join(dir, "policy.yaml")
	b, err := os.ReadFile(filepath.Join("..", "..", "policy.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pol, b, 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	code := diagnoseMain([]string{"-policy", pol, "-addr", "127.0.0.1:8080"}, &buf)
	out := buf.String()
	if !strings.Contains(out, `"topology"`) {
		t.Fatalf("output %s", out)
	}
	if strings.Contains(out, "RUSUI_WORKER_SECRET") && strings.Contains(out, os.Getenv("RUSUI_WORKER_SECRET")) && os.Getenv("RUSUI_WORKER_SECRET") != "" {
		t.Fatal("secret leaked")
	}
	_ = code
	buf.Reset()
	code = diagnoseMain([]string{"-policy", filepath.Join(dir, "missing.yaml")}, &buf)
	if code == 0 {
		t.Fatal("missing policy should fail")
	}
	if !strings.Contains(buf.String(), "misconfigured") {
		t.Fatalf("missing %s", buf.String())
	}
}

package main

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestRunnerReachesAnHTTPSPlaneWithTheCA builds the runner and runs it
// against a TLS plane stand-in: with -ca it gets through TLS to the claim
// endpoint; without a CA it refuses to start.
func TestRunnerReachesAnHTTPSPlaneWithTheCA(t *testing.T) {
	bin := t.TempDir() + "/rusui-runner"
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	claimed := make(chan struct{}, 1)
	hs := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/jobs/claim" {
			claimed <- struct{}{}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(hs.Close)
	ca := t.TempDir() + "/ca.pem"
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: hs.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "-url", hs.URL, "-repo", "o/r", "-acp", "-once", "-token", "wsec", "-ca", ca)
	cmd.Env = append(os.Environ(), "RUSUI_PLANE_CA=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("runner: %v\n%s", err, out)
	}
	select {
	case <-claimed:
	default:
		t.Fatalf("runner never reached claim over TLS:\n%s", out)
	}

	cmd = exec.Command(bin, "-url", hs.URL, "-repo", "o/r", "-acp", "-once")
	cmd.Env = append(os.Environ(), "RUSUI_PLANE_CA=")
	out, err = cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "RUSUI_PLANE_CA") {
		t.Fatalf("runner started without a CA: %v\n%s", err, out)
	}
}

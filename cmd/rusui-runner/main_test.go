package main

import (
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
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
	claimed := make(chan []string, 4)
	hs := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/jobs/claim" {
			var request struct {
				Repo  string   `json:"repo"`
				Repos []string `json:"repos"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode claim request: %v", err)
			}
			repos := request.Repos
			if request.Repo != "" {
				repos = []string{request.Repo}
			}
			claimed <- repos
			if slices.Contains(repos, "bad/r") {
				w.WriteHeader(http.StatusConflict)
				_, _ = fmt.Fprintln(w, "policy")
				return
			}
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
	if !strings.Contains(string(out), "runner: no work: no queued turn") {
		t.Fatalf("runner did not explain the empty queue:\n%s", out)
	}
	select {
	case repos := <-claimed:
		if len(repos) != 1 || repos[0] != "o/r" {
			t.Fatalf("single -repo claim %q, want [o/r]", repos)
		}
	default:
		t.Fatalf("runner never reached claim over TLS:\n%s", out)
	}

	cmd = exec.Command(bin, "-url", hs.URL, "-repo", "o/r1", "-repo", "o/r2", "-acp", "-once", "-token", "wsec", "-ca", ca)
	cmd.Env = append(os.Environ(), "RUSUI_PLANE_CA=")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("runner with repeated -repo: %v\n%s", err, out)
	}
	select {
	case repos := <-claimed:
		if strings.Join(repos, ",") != "o/r1,o/r2" {
			t.Fatalf("repeated -repo claim %q, want [o/r1 o/r2]", repos)
		}
	default:
		t.Fatalf("runner did not claim o/r1,o/r2 over TLS:\n%s", out)
	}

	cmd = exec.Command(bin, "-url", hs.URL, "-repo", "bad/r", "-acp", "-once", "-token", "wsec", "-ca", ca)
	cmd.Env = append(os.Environ(), "RUSUI_PLANE_CA=")
	out, err = cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "policy or lane disabled") {
		t.Fatalf("runner did not report a policy claim error with non-zero exit: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "-url", hs.URL, "-repo", "o/r", "-repo", "O/R", "-acp", "-once", "-token", "wsec", "-ca", ca)
	cmd.Env = append(os.Environ(), "RUSUI_PLANE_CA=")
	out, err = cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "listed more than once") {
		t.Fatalf("duplicate repositories were accepted: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "-url", hs.URL, "-repo", "o/r", "-acp", "-once")
	cmd.Env = append(os.Environ(), "RUSUI_PLANE_CA=")
	out, err = cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "RUSUI_PLANE_CA") {
		t.Fatalf("runner started without a CA: %v\n%s", err, out)
	}
}

func TestRunnerOnceExitsNonZeroWhenClaimFails(t *testing.T) {
	bin := t.TempDir() + "/rusui-runner"
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/jobs/claim" {
			w.WriteHeader(http.StatusConflict)
			_, _ = fmt.Fprintln(w, "policy")
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer hs.Close()

	cmd := exec.Command(bin, "-url", hs.URL, "-repo", "o/r", "-acp", "-once", "-token", "wsec")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "policy or lane disabled") {
		t.Fatalf("claim error did not produce a reason and non-zero exit: %v\n%s", err, out)
	}
}

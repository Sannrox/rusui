package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnoseMainExitCodes(t *testing.T) {
	for _, key := range []string{"RUSUI_GUEST", "RUSUI_MODEL_UPSTREAM", "RUSUI_XAI_API_KEY", "XAI_API_KEY", "RUSUI_ANTHROPIC_API_KEY", "ANTHROPIC_API_KEY"} {
		t.Setenv(key, "")
	}
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
	if code == 0 || !strings.Contains(out, `"name": "model_upstream"`) || !strings.Contains(out, "xAI upstream needs a model key") {
		t.Fatalf("missing model configuration was not reported: code=%d\n%s", code, out)
	}
	buf.Reset()
	code = diagnoseMain([]string{"-policy", filepath.Join(dir, "missing.yaml")}, &buf)
	if code == 0 {
		t.Fatal("missing policy should fail")
	}
	if !strings.Contains(buf.String(), "misconfigured") {
		t.Fatalf("missing %s", buf.String())
	}
}

func TestModelUpstreamNoOpProbeUsesProviderAuth(t *testing.T) {
	secret := "provider-key-must-not-appear"
	cases := []struct {
		name       string
		guest      string
		keyName    string
		provider   string
		headerName string
		status     int
		wantStatus string
	}{
		{name: "xAI ready", guest: "grok", keyName: "XAI_API_KEY", provider: "xAI", headerName: "Authorization", status: http.StatusOK, wantStatus: "ready"},
		{name: "Anthropic rejects key", guest: "claude", keyName: "RUSUI_ANTHROPIC_API_KEY", provider: "Anthropic", headerName: "x-api-key", status: http.StatusUnauthorized, wantStatus: "misconfigured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/models" {
					t.Errorf("request path = %q", r.URL.Path)
				}
				wantKey := secret
				if tc.headerName == "Authorization" {
					wantKey = "Bearer " + secret
				}
				if got := r.Header.Get(tc.headerName); got != wantKey {
					t.Errorf("%s = %q, want configured provider credential", tc.headerName, got)
				}
				if tc.provider == "Anthropic" && r.Header.Get("anthropic-version") != "2023-06-01" {
					t.Errorf("anthropic-version = %q", r.Header.Get("anthropic-version"))
				}
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprintln(w, secret)
			}))
			defer hs.Close()
			values := map[string]string{
				"RUSUI_GUEST":          tc.guest,
				"RUSUI_MODEL_UPSTREAM": hs.URL,
				tc.keyName:             secret,
			}
			check := checkModelUpstream(func(key string) string { return values[key] })
			if check.Status != tc.wantStatus || !strings.Contains(check.Detail, tc.provider) {
				t.Fatalf("check = %+v", check)
			}
			if strings.Contains(check.Detail, secret) {
				t.Fatalf("credential leaked in check: %+v", check)
			}
		})
	}
}

func TestModelUpstreamProbeDoesNotFollowRedirect(t *testing.T) {
	secret := "redirect-key-must-not-leak"
	forwarded := make(chan bool, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded <- r.Header.Get("Authorization") == "Bearer "+secret
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()
	values := map[string]string{
		"RUSUI_GUEST":          "grok",
		"RUSUI_MODEL_UPSTREAM": redirect.URL,
		"XAI_API_KEY":          secret,
	}
	check := checkModelUpstream(func(key string) string { return values[key] })
	if check.Status != "misconfigured" || !strings.Contains(check.Detail, "xAI") {
		t.Fatalf("redirect check = %+v", check)
	}
	select {
	case got := <-forwarded:
		t.Fatalf("redirect target received provider credential: %v", got)
	default:
	}
}

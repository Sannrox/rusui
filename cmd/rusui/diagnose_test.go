package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
		keyless    bool
		status     int
		wantStatus string
	}{
		{name: "xAI ready", guest: "grok", keyName: "XAI_API_KEY", provider: "xAI", headerName: "Authorization", status: http.StatusOK, wantStatus: "ready"},
		{name: "Anthropic rejects key", guest: "claude", keyName: "RUSUI_ANTHROPIC_API_KEY", provider: "Anthropic", headerName: "x-api-key", status: http.StatusUnauthorized, wantStatus: "misconfigured"},
		{name: "Anthropic gateway auth", guest: "claude", provider: "Anthropic", keyless: true, status: http.StatusOK, wantStatus: "ready"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/models" {
					t.Errorf("request path = %q", r.URL.Path)
				}
				if tc.headerName != "" {
					wantKey := secret
					if tc.headerName == "Authorization" {
						wantKey = "Bearer " + secret
					}
					if got := r.Header.Get(tc.headerName); got != wantKey {
						t.Errorf("%s = %q, want configured provider credential", tc.headerName, got)
					}
				}
				if tc.provider == "Anthropic" && r.Header.Get("anthropic-version") != "2023-06-01" {
					t.Errorf("anthropic-version = %q", r.Header.Get("anthropic-version"))
				}
				if tc.keyless && r.Header.Get("x-api-key") != "" {
					t.Errorf("keyless gateway received local key %q", r.Header.Get("x-api-key"))
				}
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprintln(w, secret)
			}))
			defer hs.Close()
			values := map[string]string{
				"RUSUI_GUEST":          tc.guest,
				"RUSUI_MODEL_UPSTREAM": hs.URL,
			}
			if tc.keyName != "" {
				values[tc.keyName] = secret
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

func TestModelGuestQuotaIsUnavailableWhenCatalogIsReady(t *testing.T) {
	secret := "provider-key-must-not-appear"
	const modelID = "grok-4.6"
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			if r.Method != http.MethodGet {
				t.Errorf("models method %s", r.Method)
			}
			w.WriteHeader(http.StatusOK)
		case "/v1/messages":
			if r.Method != http.MethodPost {
				t.Errorf("messages method %s", r.Method)
			}
			if r.Header.Get("x-api-key") != secret {
				t.Errorf("x-api-key = %q", r.Header.Get("x-api-key"))
			}
			if r.Header.Get("anthropic-version") != "2023-06-01" {
				t.Errorf("anthropic-version = %q", r.Header.Get("anthropic-version"))
			}
			var body struct {
				Model     string `json:"model"`
				MaxTokens int    `json:"max_tokens"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
				t.Errorf("body: %v", err)
			}
			if body.Model != modelID || body.MaxTokens != 1 {
				t.Errorf("probe body model=%q max_tokens=%d", body.Model, body.MaxTokens)
			}
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = fmt.Fprint(w, secret)
		default:
			t.Errorf("path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer hs.Close()
	getenv := func(key string) string {
		return map[string]string{
			"RUSUI_GUEST":             "claude",
			"RUSUI_MODEL_UPSTREAM":    hs.URL,
			"RUSUI_ANTHROPIC_API_KEY": secret,
			"RUSUI_GUEST_MODEL":       modelID,
		}[key]
	}
	list := checkModelUpstream(getenv)
	guest := checkModelGuest(getenv)
	if list.Status != "ready" {
		t.Fatalf("catalog check %+v", list)
	}
	if guest.Status != "unavailable" || !guest.Blocker || !strings.Contains(guest.Detail, modelID) || !strings.Contains(guest.Detail, "429") {
		t.Fatalf("guest check %+v", guest)
	}
	if strings.Contains(guest.Detail, secret) || strings.Contains(list.Detail, secret) {
		t.Fatalf("credential leaked: list=%+v guest=%+v", list, guest)
	}
}

func TestModelGuestUnsetIsMisconfiguredForClaude(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer hs.Close()
	getenv := func(key string) string {
		return map[string]string{
			"RUSUI_GUEST":          "claude",
			"RUSUI_MODEL_UPSTREAM": hs.URL,
		}[key]
	}
	list := checkModelUpstream(getenv)
	guest := checkModelGuest(getenv)
	if list.Status != "ready" || guest.Status != "misconfigured" || !guest.Blocker {
		t.Fatalf("list %+v guest %+v", list, guest)
	}
	if !strings.Contains(guest.Detail, "RUSUI_GUEST_MODEL") || !strings.Contains(guest.Detail, "model list") {
		t.Fatalf("detail %q", guest.Detail)
	}
}

func TestModelGuestAcceptsBoundedModelCall(t *testing.T) {
	for _, tc := range []struct{ guest, path, auth string }{
		{"claude", "/v1/messages", "x-api-key"},
		{"shikigami", "/v1/chat/completions", "Authorization"},
	} {
		t.Run(tc.guest, func(t *testing.T) {
			hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path || r.Header.Get(tc.auth) == "" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer hs.Close()
			check := checkModelGuest(func(key string) string {
				return map[string]string{"RUSUI_GUEST": tc.guest, "RUSUI_MODEL_UPSTREAM": hs.URL, "RUSUI_GUEST_MODEL": "fixture-model", "RUSUI_OPENAI_API_KEY": "fixture-key", "RUSUI_ANTHROPIC_API_KEY": "fixture-key"}[key]
			})
			if check.Status != "ready" || !strings.Contains(check.Detail, "fixture-model") {
				t.Fatalf("guest %+v", check)
			}
		})
	}
}

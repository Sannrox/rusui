package server

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
)

func TestModelProxySwapsTurnToken(t *testing.T) {
	var sawAuth, sawPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawPath = r.URL.Path
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(up.Close)
	origin, err := url.Parse(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	e, _, tok := leasedTurn(t)
	hs := httptest.NewServer((&Server{Eng: e, ModelKey: "plane-secret", ModelOrigin: origin}).Handler())
	t.Cleanup(hs.Close)

	req, err := http.NewRequest("POST", hs.URL+"/model-proxy/v1/chat/completions", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("code %d %s", resp.StatusCode, body)
	}
	if sawAuth != "Bearer plane-secret" {
		t.Fatalf("upstream auth %q", sawAuth)
	}
	if strings.Contains(sawAuth, tok) {
		t.Fatal("turn token leaked")
	}
	if sawPath != "/v1/chat/completions" {
		t.Fatalf("path %q", sawPath)
	}
}

func TestModelProxyRejectsBadTokenAndMissingKey(t *testing.T) {
	e, _, tok := leasedTurn(t)
	origin, _ := url.Parse("http://127.0.0.1:1")
	hs := httptest.NewServer((&Server{Eng: e, ModelKey: "k", ModelOrigin: origin}).Handler())
	t.Cleanup(hs.Close)
	req, _ := http.NewRequest("POST", hs.URL+"/model-proxy/v1/x", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer deadbeef")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("code %d", resp.StatusCode)
	}
	hs2 := httptest.NewServer((&Server{Eng: e}).Handler())
	t.Cleanup(hs2.Close)
	req, _ = http.NewRequest("POST", hs2.URL+"/model-proxy/v1/x", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("code %d", resp.StatusCode)
	}
}

func TestModelProxyRejectsPrepareExpiredAndRequiresTLS(t *testing.T) {
	e, clk, tok := leasedTurn(t)
	origin, _ := url.Parse("http://127.0.0.1:1")
	hs := httptest.NewServer((&Server{Eng: e, ModelKey: "k", ModelOrigin: origin, GuestHTTPSOnly: true}).Handler())
	t.Cleanup(hs.Close)

	prep, _, err := e.IssuePrepareGrant("example/test-repo")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", hs.URL+"/model-proxy/v1/x", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+prep)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("prepare %d", resp.StatusCode)
	}

	req = httptest.NewRequest("POST", "/model-proxy/v1/x", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.RemoteAddr = "172.17.0.2:9"
	rr := httptest.NewRecorder()
	(&Server{Eng: e, ModelKey: "k", ModelOrigin: origin, GuestHTTPSOnly: true}).Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("http guest %d", rr.Code)
	}

	clk.Advance(engine.GrantTTL + time.Second)
	req, _ = http.NewRequest("POST", hs.URL+"/model-proxy/v1/x", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired %d", resp.StatusCode)
	}
}

func TestModelProxyTLSIdentity(t *testing.T) {
	var sawAuth bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization") != ""
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(up.Close)
	origin, _ := url.Parse(up.URL)
	e, _, tok := leasedTurn(t)
	hs := httptest.NewTLSServer((&Server{Eng: e, ModelKey: "plane-secret", ModelOrigin: origin, GuestHTTPSOnly: true}).Handler())
	t.Cleanup(hs.Close)

	req, _ := http.NewRequest("POST", hs.URL+"/model-proxy/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := hs.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !sawAuth {
		t.Fatalf("trusted tls %d saw=%v", resp.StatusCode, sawAuth)
	}

	untrusted := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: x509.NewCertPool()}}}
	req, _ = http.NewRequest("POST", hs.URL+"/model-proxy/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	if _, err := untrusted.Do(req); err == nil {
		t.Fatal("untrusted endpoint accepted grant")
	}
}

func leasedTurn(t *testing.T) (*engine.Engine, *clock.Fake, string) {
	t.Helper()
	e, clk, _ := eventEnv(t)
	now := clk.T.UTC().Format(time.RFC3339Nano)
	res, err := e.Store.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1,'review','example/test-repo',8,'issue','open',?)`, now)
	if err != nil {
		t.Fatal(err)
	}
	sid, _ := res.LastInsertId()
	if _, err := e.Store.DB.Exec(`INSERT INTO turns (session_id, lane, state) VALUES (?, 'review', 'leased')`, sid); err != nil {
		t.Fatal(err)
	}
	var turnID int64
	if err := e.Store.DB.QueryRow(`SELECT id FROM turns WHERE session_id=?`, sid).Scan(&turnID); err != nil {
		t.Fatal(err)
	}
	tok, _, err := issueTurnToken(e.Store, turnID, 1, clk.T)
	if err != nil {
		t.Fatal(err)
	}
	return e, clk, tok
}

// upstreamSaw proxies one guest request through a plane configured with srv
// and returns the headers the upstream received.
func upstreamSaw(t *testing.T, srv *Server, guestHeaders map[string]string) http.Header {
	t.Helper()
	var saw http.Header
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = r.Header.Clone()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(up.Close)
	origin, err := url.Parse(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	e, _, tok := leasedTurn(t)
	srv.Eng = e
	srv.ModelOrigin = origin
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	req, err := http.NewRequest("POST", hs.URL+"/model-proxy/v1/messages", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	for k, v := range guestHeaders {
		req.Header.Set(k, strings.ReplaceAll(v, "$GRANT", tok))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("code %d %s", resp.StatusCode, body)
	}
	for k, vs := range saw {
		for _, v := range vs {
			if strings.Contains(v, tok) {
				t.Fatalf("grant reached upstream in %s", k)
			}
		}
	}
	return saw
}

func TestModelProxyAnthropicSendsAPIKeyHeader(t *testing.T) {
	saw := upstreamSaw(t, &Server{ModelProvider: ProviderAnthropic, ModelKey: "sk-ant-plane"},
		map[string]string{"X-Api-Key": "$GRANT", "Anthropic-Version": "2023-06-01"})
	if saw.Get("X-Api-Key") != "sk-ant-plane" || saw.Get("Authorization") != "" {
		t.Fatalf("upstream auth x-api-key=%q authorization=%q", saw.Get("X-Api-Key"), saw.Get("Authorization"))
	}
	if saw.Get("Anthropic-Version") != "2023-06-01" {
		t.Fatal("anthropic-version not passed through")
	}
}

func TestModelProxyKeylessOperatorUpstream(t *testing.T) {
	// A CLI proxy on the plane host holds the operator's login itself.
	saw := upstreamSaw(t, &Server{ModelProvider: ProviderAnthropic}, nil)
	if saw.Get("X-Api-Key") != "" || saw.Get("Authorization") != "" {
		t.Fatalf("keyless upstream got credentials %v", saw)
	}
}

func TestModelConfigFromEnv(t *testing.T) {
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	c, err := ModelConfigFromEnv(env(map[string]string{"RUSUI_XAI_API_KEY": "x"}))
	if err != nil || c.Provider != ProviderXAI || c.Key != "x" || c.Origin != nil {
		t.Fatalf("default %+v %v", c, err)
	}
	c, err = ModelConfigFromEnv(env(map[string]string{
		"RUSUI_GUEST": "claude", "ANTHROPIC_API_KEY": "a", "RUSUI_ANTHROPIC_API_KEY": "b",
		"RUSUI_MODEL_UPSTREAM": "http://127.0.0.1:8317", "XAI_API_KEY": "x",
	}))
	if err != nil || c.Provider != ProviderAnthropic || c.Key != "b" || c.Origin.String() != "http://127.0.0.1:8317" {
		t.Fatalf("claude %+v %v", c, err)
	}
	for _, bad := range []map[string]string{
		{"RUSUI_GUEST": "codex"},
		{"RUSUI_MODEL_UPSTREAM": "127.0.0.1:8317"},
		{"RUSUI_MODEL_UPSTREAM": "file:///etc/passwd"},
	} {
		if _, err := ModelConfigFromEnv(env(bad)); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
}

func TestModelUpstreamErrorsNeverEchoCredentials(t *testing.T) {
	for _, raw := range []string{"ftp://op:s3cret-key@gateway", "http://op:s3cret-key@[bad", "op:s3cret-key@gateway"} {
		_, err := ModelConfigFromEnv(func(k string) string {
			if k == "RUSUI_MODEL_UPSTREAM" {
				return raw
			}
			return ""
		})
		if err == nil || strings.Contains(err.Error(), "s3cret-key") {
			t.Fatalf("%q: %v", raw, err)
		}
	}
}

// A failing upstream must not put the provider key or the guest grant in
// the plane log or the guest-visible response.
func TestModelProxyFailureLeaksNoSecrets(t *testing.T) {
	var logs strings.Builder
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	e, _, tok := leasedTurn(t)
	origin, _ := url.Parse("http://127.0.0.1:1")
	hs := httptest.NewServer((&Server{Eng: e, ModelKey: "sk-provider-secret", ModelOrigin: origin}).Handler())
	t.Cleanup(hs.Close)
	req, _ := http.NewRequest("POST", hs.URL+"/model-proxy/v1/messages", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("code %d %s", resp.StatusCode, body)
	}
	if logs.Len() == 0 {
		t.Fatal("proxy failure was not logged")
	}
	for _, secret := range []string{"sk-provider-secret", tok} {
		if strings.Contains(logs.String(), secret) || strings.Contains(string(body), secret) {
			t.Fatalf("secret leaked:\nlog: %s\nbody: %s", logs.String(), body)
		}
	}
}

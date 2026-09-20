package server

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	hs2 := httptest.NewServer((&Server{Eng: e, ModelOrigin: origin}).Handler())
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

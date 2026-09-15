package gh

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestInstallationTokensMintsAndCaches(t *testing.T) {
	key := testKey(t)
	var posts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/9/access_tokens" || r.Method != http.MethodPost {
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") == "" {
			t.Fatal("missing jwt")
		}
		posts.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_install",
			"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
		})
	}))
	t.Cleanup(ts.Close)
	src := &InstallationTokens{
		AppID: 1, InstallationID: 9, Key: key, BaseURL: ts.URL, HTTP: ts.Client(),
		Now: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}
	tok, err := src.Token()
	if err != nil || tok != "ghs_install" {
		t.Fatalf("%q %v", tok, err)
	}
	tok, err = src.Token()
	if err != nil || tok != "ghs_install" {
		t.Fatal(err)
	}
	if posts.Load() != 1 {
		t.Fatalf("posts %d", posts.Load())
	}
}

func TestParseAppPrivateKeyPKCS1(t *testing.T) {
	k := testKey(t)
	b := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
	got, err := ParseAppPrivateKey(b)
	if err != nil || got.N.Cmp(k.N) != 0 {
		t.Fatalf("%v", err)
	}
	if _, err := ParseAppPrivateKey([]byte("not-pem")); err == nil {
		t.Fatal("accepted junk")
	}
}

func TestLoadAppFromEnv(t *testing.T) {
	k := testKey(t)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
	env := map[string]string{
		"RUSUI_GITHUB_APP_ID":              "42",
		"RUSUI_GITHUB_APP_PRIVATE_KEY":     string(pemBytes),
		"RUSUI_GITHUB_APP_INSTALLATION_ID": "7",
	}
	app, err := LoadAppFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if app.AppID != 42 || app.InstallationID != 7 {
		t.Fatalf("%+v", app)
	}
	if _, err := LoadAppFromEnv(func(string) string { return "" }); err == nil {
		t.Fatal("empty env")
	}
}

func TestAPIUsesTokenSource(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghs_install" {
			t.Fatalf("auth %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"default_branch":"main","full_name":"o/r"}`))
	}))
	t.Cleanup(ts.Close)
	api := NewAPI("pat-unused", nil)
	api.BaseURL = ts.URL
	api.Tokens = StaticToken("ghs_install")
	if err := api.get("/repos/o/r", &ghRepo{}); err != nil {
		t.Fatal(err)
	}
}

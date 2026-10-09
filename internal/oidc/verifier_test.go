package oidc_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/sannrox/rusui/internal/oidc"
	"github.com/sannrox/rusui/internal/oidc/oidctest"
)

func TestVerifierRequiresOperatorAccessClaims(t *testing.T) {
	p := oidctest.New(t)
	ctx := t.Context()
	v, err := oidc.NewVerifier(ctx, oidc.Config{Metadata: oidc.Metadata{Issuer: p.Server.URL, Audience: "rusui", ClientID: "cli", Scopes: []string{"openid"}}, Subject: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		modify func(*jwt.RegisteredClaims)
		want   bool
	}{
		{"valid", func(*jwt.RegisteredClaims) {}, true},
		{"issuer", func(c *jwt.RegisteredClaims) { c.Issuer = "https://other.example" }, false},
		{"audience", func(c *jwt.RegisteredClaims) { c.Audience = jwt.ClaimStrings{"cli"} }, false},
		{"subject", func(c *jwt.RegisteredClaims) { c.Subject = "another" }, false},
		{"expired", func(c *jwt.RegisteredClaims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute)) }, false},
		{"missing expiry", func(c *jwt.RegisteredClaims) { c.ExpiresAt = nil }, false},
		{"not yet valid", func(c *jwt.RegisteredClaims) { c.NotBefore = jwt.NewNumericDate(time.Now().Add(time.Hour)) }, false},
		{"future issued", func(c *jwt.RegisteredClaims) { c.IssuedAt = jwt.NewNumericDate(time.Now().Add(time.Hour)) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := p.Claims()
			tc.modify(&c)
			if got := v.Valid(p.Token(t, c)); got != tc.want {
				t.Fatalf("accepted=%v want=%v", got, tc.want)
			}
		})
	}
	for _, method := range []jwt.SigningMethod{jwt.SigningMethodNone, jwt.SigningMethodHS256} {
		tok := jwt.NewWithClaims(method, p.Claims())
		tok.Header["kid"] = "key-1"
		var key any = []byte("secret")
		if method == jwt.SigningMethodNone {
			key = jwt.UnsafeAllowNoneSignatureType
		}
		raw, err := tok.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		if v.Valid(raw) {
			t.Fatal("unsafe algorithm accepted")
		}
	}
	valid := p.Token(t, p.Claims())
	before := p.Requests.Load()
	p.Offline.Store(true)
	if !v.Valid(valid) || p.Requests.Load() != before {
		t.Fatal("verification fetched issuer or discarded cached keys")
	}
	p.Kid.Store("key-2")
	unknown := p.Token(t, p.Claims())
	if v.Valid(unknown) {
		t.Fatal("unknown key accepted")
	}
	// The failed refresh cannot erase the previously usable key set.
	if !v.Valid(valid) {
		t.Fatal("refresh failure revoked cached key")
	}
	p.Offline.Store(false)
}

func TestVerifierRejectsUnusableKeysAtStartup(t *testing.T) {
	p := oidctest.New(t)
	p.Offline.Store(true)
	if _, err := oidc.NewVerifier(context.Background(), oidc.Config{Metadata: oidc.Metadata{Issuer: p.Server.URL, Audience: "rusui", ClientID: "cli"}, Subject: "operator"}); err == nil {
		t.Fatal("started without trusted keys")
	}
}

func TestVerifierAcceptsP256(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var issuer string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/keys" {
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kid": "ec", "kty": "EC", "crv": "P-256", "x": base64.RawURLEncoding.EncodeToString(key.X.FillBytes(make([]byte, 32))), "y": base64.RawURLEncoding.EncodeToString(key.Y.FillBytes(make([]byte, 32)))}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys"})
	}))
	defer hs.Close()
	issuer = hs.URL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	v, err := oidc.NewVerifier(ctx, oidc.Config{Metadata: oidc.Metadata{Issuer: issuer, Audience: "rusui", ClientID: "cli"}, Subject: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.RegisteredClaims{Issuer: issuer, Subject: "operator", Audience: jwt.ClaimStrings{"rusui"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))})
	tok.Header["kid"] = "ec"
	raw, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Valid(raw) {
		t.Fatal("valid P256 token refused")
	}
}

func TestVerifierRefreshesUnknownKey(t *testing.T) {
	p := oidctest.New(t)
	ctx := t.Context()
	v, err := oidc.NewVerifier(ctx, oidc.Config{Metadata: oidc.Metadata{Issuer: p.Server.URL, Audience: "rusui", ClientID: "cli"}, Subject: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	p.Kid.Store("rotated")
	raw := p.Token(t, p.Claims())
	if v.Valid(raw) {
		t.Fatal("unknown key accepted before refresh")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v.Valid(raw) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("key refresh did not make new signing key usable")
}

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
	"strings"
	"sync/atomic"
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
			if got := operatorValid(v, p.Token(t, c)); got != tc.want {
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
		if operatorValid(v, raw) {
			t.Fatal("unsafe algorithm accepted")
		}
	}
	valid := p.Token(t, p.Claims())
	before := p.Requests.Load()
	p.Offline.Store(true)
	if !operatorValid(v, valid) || p.Requests.Load() != before {
		t.Fatal("verification fetched issuer or discarded cached keys")
	}
	p.Kid.Store("key-2")
	unknown := p.Token(t, p.Claims())
	if operatorValid(v, unknown) {
		t.Fatal("unknown key accepted")
	}
	// The failed refresh cannot erase the previously usable key set.
	if !operatorValid(v, valid) {
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
	if !operatorValid(v, raw) {
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
	if operatorValid(v, raw) {
		t.Fatal("unknown key accepted before refresh")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if operatorValid(v, raw) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("key refresh did not make new signing key usable")
}

type vouchedClaims struct {
	jwt.RegisteredClaims
	Act map[string]string `json:"act,omitempty"`
}

// ADR 0088 D7: a vouched subject's token carries iat and exp at most an
// hour apart, iat at most a minute ahead, and is refused once the cached
// keys are an hour past their last fetch. The operator subject keeps ADR 0074.
func TestVerifierBoundsVouchedSubjects(t *testing.T) {
	p := oidctest.New(t)
	now := time.Now()
	clock := atomic.Pointer[time.Time]{}
	clock.Store(&now)
	v, err := oidc.NewVerifier(t.Context(), oidc.Config{Metadata: oidc.Metadata{Issuer: p.Server.URL, Audience: "rusui", ClientID: "cli"}, Subject: "operator", Now: func() time.Time { return *clock.Load() }})
	if err != nil {
		t.Fatal(err)
	}
	at := func(iat, exp time.Duration) jwt.RegisteredClaims {
		c := p.Claims()
		c.Subject = "u-alice"
		c.IssuedAt = jwt.NewNumericDate(now.Add(iat))
		c.ExpiresAt = jwt.NewNumericDate(now.Add(exp))
		return c
	}
	for _, tc := range []struct {
		name   string
		claims jwt.RegisteredClaims
		want   bool
	}{
		{"one hour", at(-time.Minute, 59*time.Minute), true},
		{"iat inside skew", at(30*time.Second, 50*time.Minute), true},
		{"lifetime over one hour", at(-time.Minute, time.Hour), false},
		{"iat over a minute ahead", at(2*time.Minute, 30*time.Minute), false},
		{"no iat", func() jwt.RegisteredClaims { c := at(0, time.Minute); c.IssuedAt = nil; return c }(), false},
		{"no exp", func() jwt.RegisteredClaims { c := at(0, time.Minute); c.ExpiresAt = nil; return c }(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, ok := v.Verify(p.Token(t, tc.claims))
			if ok != tc.want || (ok && (!id.Vouched || id.Subject != "u-alice")) {
				t.Fatalf("Verify = %+v, %v; want %v", id, ok, tc.want)
			}
		})
	}
	// The act claim is returned as the delegate, not as the subject.
	id, ok := v.Verify(p.Token(t, vouchedClaims{RegisteredClaims: at(0, 30*time.Minute), Act: map[string]string{"sub": "svc"}}))
	if !ok || id.Subject != "u-alice" || id.Delegate != "svc" {
		t.Fatalf("delegated token = %+v, %v", id, ok)
	}

	// Issuer unreachable: keys age from the last successful fetch.
	p.Offline.Store(true)
	operator := p.Token(t, jwt.RegisteredClaims{Issuer: p.Server.URL, Subject: "operator", Audience: jwt.ClaimStrings{"rusui"}, ExpiresAt: jwt.NewNumericDate(now.Add(3 * time.Hour))})
	later := now.Add(59 * time.Minute)
	clock.Store(&later)
	fresh := p.Token(t, jwt.RegisteredClaims{Issuer: p.Server.URL, Subject: "u-alice", Audience: jwt.ClaimStrings{"rusui"}, IssuedAt: jwt.NewNumericDate(later), ExpiresAt: jwt.NewNumericDate(later.Add(30 * time.Minute))})
	if _, ok := v.Verify(fresh); !ok {
		t.Fatal("vouched token refused inside the key-age bound")
	}
	later = now.Add(61 * time.Minute)
	clock.Store(&later)
	if _, ok := v.Verify(fresh); ok {
		t.Fatal("vouched token accepted on keys older than one hour")
	}
	if !operatorValid(v, operator) {
		t.Fatal("operator subject lost its cached-key rule")
	}
}

// operatorValid reports whether raw authenticates the configured operator subject.
func operatorValid(v *oidc.Verifier, raw string) bool {
	id, ok := v.Verify(raw)
	return ok && !id.Vouched
}

// A non-object act claim keeps working for the operator subject (ADR 0074)
// and refuses a vouched subject rather than recording a garbled delegate.
func TestVerifierActClaimShapes(t *testing.T) {
	p := oidctest.New(t)
	v, err := oidc.NewVerifier(t.Context(), oidc.Config{Metadata: oidc.Metadata{Issuer: p.Server.URL, Audience: "rusui", ClientID: "cli"}, Subject: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	type stringAct struct {
		jwt.RegisteredClaims
		Act string `json:"act"`
	}
	if !operatorValid(v, p.Token(t, stringAct{RegisteredClaims: p.Claims(), Act: "svc"})) {
		t.Fatal("operator token with a string act refused")
	}
	c := p.Claims()
	c.Subject, c.ExpiresAt = "u-alice", jwt.NewNumericDate(time.Now().Add(30*time.Minute))
	if _, ok := v.Verify(p.Token(t, stringAct{RegisteredClaims: c, Act: "svc"})); ok {
		t.Fatal("vouched token with a string act accepted")
	}
	if _, ok := v.Verify(p.Token(t, vouchedClaims{RegisteredClaims: c, Act: map[string]string{"sub": strings.Repeat("s", 257)}})); ok {
		t.Fatal("oversized act sub accepted")
	}
}

// Package oidctest provides an isolated signing issuer for authentication tests.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Provider struct {
	Server       *httptest.Server
	Key          *rsa.PrivateKey
	Requests     atomic.Int64
	Offline      atomic.Bool
	Kid          atomic.Value
	TokenHandler http.HandlerFunc
}

func New(t *testing.T) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{Key: key}
	p.Kid.Store("key-1")
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.Requests.Add(1)
		if p.Offline.Load() {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": p.Server.URL, "authorization_endpoint": p.Server.URL + "/authorize", "token_endpoint": p.Server.URL + "/token", "jwks_uri": p.Server.URL + "/keys"})
		case "/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kid": p.Kid.Load().(string), "kty": "RSA", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(p.Key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(p.Key.E)).Bytes())}}})
		case "/token":
			if p.TokenHandler == nil {
				http.Error(w, "not configured", 500)
				return
			}
			p.TokenHandler(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(p.Server.Close)
	return p
}

func (p *Provider) Claims() jwt.RegisteredClaims {
	return jwt.RegisteredClaims{Issuer: p.Server.URL, Subject: "operator", Audience: jwt.ClaimStrings{"rusui"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)), IssuedAt: jwt.NewNumericDate(time.Now().Add(-time.Minute))}
}

func (p *Provider) Token(t *testing.T, claims jwt.RegisteredClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = p.Kid.Load().(string)
	raw, err := token.SignedString(p.Key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

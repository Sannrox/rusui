package oidc

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Config struct {
	Metadata
	Subject string
}

func ConfigFromEnv() (Config, error) {
	c := Config{Metadata: Metadata{Issuer: os.Getenv("RUSUI_OIDC_ISSUER"), Audience: os.Getenv("RUSUI_OIDC_AUDIENCE"), ClientID: os.Getenv("RUSUI_OIDC_CLIENT_ID"), Scopes: strings.Fields(os.Getenv("RUSUI_OIDC_SCOPES"))}, Subject: os.Getenv("RUSUI_OIDC_SUBJECT")}
	if c.Issuer == "" && c.Audience == "" && c.ClientID == "" && c.Subject == "" && len(c.Scopes) == 0 {
		return c, nil
	}
	if c.Issuer == "" || c.Audience == "" || c.ClientID == "" || c.Subject == "" {
		return c, errors.New("OIDC: issuer, audience, client ID, and subject are all required")
	}
	if len(c.Scopes) == 0 {
		c.Scopes = []string{"openid"}
	}
	_, err := URL(c.Issuer)
	return c, err
}

type signingKey struct {
	alg string
	key any
}

// Verifier checks only cached keys on the request path. Refreshes replace keys atomically.
type Verifier struct {
	config  Config
	client  *http.Client
	jwks    string
	mu      sync.RWMutex
	keys    map[string]signingKey
	refresh chan struct{}
}

func NewVerifier(ctx context.Context, c Config) (*Verifier, error) {
	if c.Issuer == "" || c.Audience == "" || c.ClientID == "" || c.Subject == "" {
		return nil, errors.New("OIDC: incomplete configuration")
	}
	client := HTTPClient()
	d, err := Discover(ctx, client, c.Issuer)
	if err != nil {
		return nil, err
	}
	v := &Verifier{config: c, client: client, jwks: d.JWKSURI, refresh: make(chan struct{}, 1)}
	if err = v.loadKeys(ctx); err != nil {
		return nil, err
	}
	go v.refreshKeys(ctx)
	return v, nil
}

func (v *Verifier) Metadata() Metadata { return v.config.Metadata }

func (v *Verifier) Valid(raw string) bool {
	if len(raw) > 32<<10 {
		return false
	}
	_, err := jwt.ParseWithClaims(raw, &jwt.RegisteredClaims{}, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		v.mu.RLock()
		k, ok := v.keys[kid]
		v.mu.RUnlock()
		if !ok {
			select {
			case v.refresh <- struct{}{}:
			default:
			}
			return nil, errors.New("unknown signing key")
		}
		if k.alg != token.Method.Alg() {
			return nil, errors.New("signing algorithm mismatch")
		}
		return k.key, nil
	}, jwt.WithValidMethods([]string{"RS256", "ES256"}), jwt.WithIssuer(v.config.Issuer), jwt.WithAudience(v.config.Audience), jwt.WithSubject(v.config.Subject), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	return err == nil
}

func (v *Verifier) refreshKeys(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-v.refresh:
			if time.Since(last) < 30*time.Second {
				continue
			}
		}
		last = time.Now()
		// Failure keeps the last usable key set. The token that requested refresh was refused.
		_ = v.loadKeys(ctx)
	}
}

func (v *Verifier) loadKeys(ctx context.Context) error {
	var set struct {
		Keys []struct {
			KID    string   `json:"kid"`
			KTY    string   `json:"kty"`
			Alg    string   `json:"alg"`
			Use    string   `json:"use"`
			KeyOps []string `json:"key_ops"`
			N      string   `json:"n"`
			E      string   `json:"e"`
			Crv    string   `json:"crv"`
			X      string   `json:"x"`
			Y      string   `json:"y"`
		} `json:"keys"`
	}
	if err := GetJSON(ctx, v.client, v.jwks, &set); err != nil {
		return err
	}
	keys := make(map[string]signingKey)
	for _, j := range set.Keys {
		if j.KID == "" || (j.Use != "" && j.Use != "sig") {
			continue
		}
		if len(j.KeyOps) > 0 {
			verify := false
			for _, op := range j.KeyOps {
				if op == "verify" {
					verify = true
				}
			}
			if !verify {
				continue
			}
		}
		var k signingKey
		switch j.KTY {
		case "RSA":
			if j.Alg != "" && j.Alg != "RS256" {
				continue
			}
			n, e1 := base64.RawURLEncoding.DecodeString(j.N)
			e, e2 := base64.RawURLEncoding.DecodeString(j.E)
			if e1 != nil || e2 != nil || len(e) == 0 || len(e) > 4 || len(n) < 256 {
				continue
			}
			exp := 0
			for _, b := range e {
				exp = exp<<8 | int(b)
			}
			if exp < 3 || exp%2 == 0 {
				continue
			}
			k = signingKey{"RS256", &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exp}}
		case "EC":
			if j.Crv != "P-256" || (j.Alg != "" && j.Alg != "ES256") {
				continue
			}
			x, e1 := base64.RawURLEncoding.DecodeString(j.X)
			y, e2 := base64.RawURLEncoding.DecodeString(j.Y)
			if e1 != nil || e2 != nil || len(x) != 32 || len(y) != 32 {
				continue
			}
			pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
			encoded := append([]byte{4}, x...)
			encoded = append(encoded, y...)
			if _, err := ecdh.P256().NewPublicKey(encoded); err != nil {
				continue
			}
			k = signingKey{"ES256", pub}
		default:
			continue
		}
		if _, duplicate := keys[j.KID]; duplicate {
			return errors.New("OIDC: duplicate signing key ID")
		}
		keys[j.KID] = k
	}
	if len(keys) == 0 {
		return errors.New("OIDC: no usable signing keys")
	}
	v.mu.Lock()
	v.keys = keys
	v.mu.Unlock()
	return nil
}

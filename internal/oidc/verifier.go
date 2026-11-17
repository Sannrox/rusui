package oidc

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
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
	// Now is the plane clock for expiry and key-age checks. Nil means
	// time.Now; tests set a fake clock.
	Now func() time.Time
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

// Vouched-subject bounds (ADR 0088 D7). Fixed values, not settings.
const (
	VouchedMaxLifetime = time.Hour
	VouchedMaxKeyAge   = time.Hour
	VouchedIssuedSkew  = time.Minute
)

// Verifier checks only cached keys on the request path. Refreshes replace keys atomically.
type Verifier struct {
	config  Config
	client  *http.Client
	jwks    string
	mu      sync.RWMutex
	keys    map[string]signingKey
	fetched time.Time // last successful key fetch
	refresh chan struct{}
	now     func() time.Time
}

// Identity is the subject of a verified access token.
type Identity struct {
	Subject string
	// Delegate is the sub of the token's act claim: the service acting for
	// Subject. It is recorded, never used for access (ADR 0088 D2).
	Delegate string
	// Vouched is true for any subject other than the configured operator
	// subject. Such a token already passed the ADR 0088 D7 bounds; the
	// caller still decides whether the subject is mapped.
	Vouched bool
}

// maxDelegate bounds the recorded act sub; a longer one refuses the token.
const maxDelegate = 256

type accessClaims struct {
	jwt.RegisteredClaims
	// Act stays raw so a non-object act never fails the parse: only an
	// object's sub is read, and only for vouched subjects.
	Act json.RawMessage `json:"act,omitempty"`
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
	v := &Verifier{config: c, client: client, jwks: d.JWKSURI, refresh: make(chan struct{}, 1), now: c.Now}
	if v.now == nil {
		v.now = time.Now
	}
	if err = v.loadKeys(ctx); err != nil {
		return nil, err
	}
	go v.refreshKeys(ctx)
	return v, nil
}

func (v *Verifier) Metadata() Metadata { return v.config.Metadata }

// Verify checks signature, algorithm, issuer, audience and expiry against
// cached keys. The configured operator subject keeps the ADR 0074 rules.
// Any other subject must also carry iat and exp no more than an hour apart,
// iat at most a minute ahead, and keys fetched within the last hour.
func (v *Verifier) Verify(raw string) (Identity, bool) {
	if len(raw) > 32<<10 {
		return Identity{}, false
	}
	var c accessClaims
	_, err := jwt.ParseWithClaims(raw, &c, func(token *jwt.Token) (any, error) {
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
	}, jwt.WithValidMethods([]string{"RS256", "ES256"}), jwt.WithIssuer(v.config.Issuer), jwt.WithAudience(v.config.Audience), jwt.WithExpirationRequired(), jwt.WithTimeFunc(v.now))
	if err != nil || c.Subject == "" {
		return Identity{}, false
	}
	now := v.now()
	id := Identity{Subject: c.Subject, Vouched: c.Subject != v.config.Subject}
	if !id.Vouched {
		// ADR 0074: an iat, when present, may not be in the future.
		return id, c.IssuedAt == nil || !c.IssuedAt.After(now)
	}
	if c.IssuedAt == nil {
		return Identity{}, false
	}
	iat, exp := c.IssuedAt.Time, c.ExpiresAt.Time
	if exp.Sub(iat) > VouchedMaxLifetime || iat.After(now.Add(VouchedIssuedSkew)) {
		return Identity{}, false
	}
	v.mu.RLock()
	fetched := v.fetched
	v.mu.RUnlock()
	if now.Sub(fetched) > VouchedMaxKeyAge {
		return Identity{}, false
	}
	if len(c.Act) > 0 {
		var act struct {
			Sub string `json:"sub"`
		}
		if json.Unmarshal(c.Act, &act) != nil || len(act.Sub) > maxDelegate {
			return Identity{}, false
		}
		id.Delegate = act.Sub
	}
	return id, true
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
	v.fetched = v.now()
	v.mu.Unlock()
	return nil
}

// Package oidc implements the optional single-operator CLI identity boundary.
package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Metadata contains only public client configuration, never the operator subject.
type Metadata struct {
	Issuer   string   `json:"issuer"`
	Audience string   `json:"audience"`
	ClientID string   `json:"client_id"`
	Scopes   []string `json:"scopes"`
}

type Discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// URL permits HTTPS or literal loopback HTTP. Credentials and fragments are refused.
func URL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("OIDC: invalid URL")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || ip == nil || !ip.IsLoopback() {
			return nil, errors.New("OIDC: HTTPS required outside loopback")
		}
	}
	return u, nil
}

// HTTPClient bounds requests and refuses redirects, including credential-bearing ones.
func HTTPClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func GetJSON(ctx context.Context, client *http.Client, raw string, dst any) error {
	if _, err := URL(raw); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("OIDC: HTTP %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(dst)
}

func Discover(ctx context.Context, client *http.Client, issuer string) (Discovery, error) {
	var d Discovery
	if err := GetJSON(ctx, client, strings.TrimRight(issuer, "/")+"/.well-known/openid-configuration", &d); err != nil {
		return d, err
	}
	if d.Issuer != issuer {
		return d, errors.New("OIDC: discovery issuer mismatch")
	}
	for _, endpoint := range []string{d.AuthorizationEndpoint, d.TokenEndpoint, d.JWKSURI} {
		if _, err := URL(endpoint); err != nil {
			return d, err
		}
	}
	return d, nil
}

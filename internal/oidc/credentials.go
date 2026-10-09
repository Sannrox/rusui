package oidc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Credentials struct {
	Server        string    `json:"server"`
	Issuer        string    `json:"issuer"`
	TokenEndpoint string    `json:"token_endpoint"`
	ClientID      string    `json:"client_id"`
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"refresh_token,omitempty"`
	ExpiresAt     time.Time `json:"expires_at"`
}

func ServerURL(raw string) (string, error) {
	u, err := URL(raw)
	if err != nil {
		return "", err
	}
	if u.RawQuery != "" {
		return "", errors.New("OIDC: server URL cannot contain a query")
	}
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]"
		}
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func credentialPath(server string) (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
	}
	if !filepath.IsAbs(base) {
		return "", errors.New("OIDC: config directory must be absolute")
	}
	sum := sha256.Sum256([]byte(server))
	return filepath.Join(base, "rusui", "credentials", hex.EncodeToString(sum[:])+".json"), nil
}

func LoadCredentials(server string) (Credentials, error) {
	var c Credentials
	normalized, err := ServerURL(server)
	if err != nil {
		return c, err
	}
	path, err := credentialPath(normalized)
	if err != nil {
		return c, err
	}
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return c, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return c, errors.New("OIDC: credential file must be private (0600)")
	}
	if err = json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&c); err != nil {
		return c, errors.New("OIDC: invalid credential file; run rusui login")
	}
	if c.Server != normalized || c.Issuer == "" || c.ClientID == "" || c.AccessToken == "" || c.ExpiresAt.IsZero() {
		return c, errors.New("OIDC: incomplete credentials; run rusui login")
	}
	if _, err = URL(c.Issuer); err != nil {
		return c, err
	}
	if _, err = URL(c.TokenEndpoint); err != nil {
		return c, err
	}
	return c, nil
}

func SaveCredentials(c Credentials) error {
	normalized, err := ServerURL(c.Server)
	if err != nil {
		return err
	}
	c.Server = normalized
	path, err := credentialPath(normalized)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".login-*")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if err = json.NewEncoder(f).Encode(c); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func Logout(server string) error {
	normalized, err := ServerURL(server)
	if err != nil {
		return err
	}
	path, err := credentialPath(normalized)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// SavedToken refreshes only against the endpoint pinned by the original login.
func SavedToken(ctx context.Context, server string) (string, error) {
	c, err := LoadCredentials(server)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if time.Until(c.ExpiresAt) > 30*time.Second {
		return c.AccessToken, nil
	}
	if c.RefreshToken == "" {
		return "", errors.New("OIDC: login expired; run rusui login")
	}
	tokens, err := Exchange(ctx, HTTPClient(), c.TokenEndpoint, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {c.RefreshToken}, "client_id": {c.ClientID}})
	if err != nil {
		return "", fmt.Errorf("OIDC: refresh failed; run rusui login: %w", err)
	}
	c.AccessToken = tokens.AccessToken
	c.ExpiresAt = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	if tokens.RefreshToken != "" {
		c.RefreshToken = tokens.RefreshToken
	}
	if err = SaveCredentials(c); err != nil {
		return "", err
	}
	return c.AccessToken, nil
}

type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
}

func Exchange(ctx context.Context, client *http.Client, endpoint string, values url.Values) (Tokens, error) {
	var tokens Tokens
	if _, err := URL(endpoint); err != nil {
		return tokens, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return tokens, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := client.Do(req)
	if err != nil {
		return tokens, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return tokens, fmt.Errorf("token endpoint HTTP %d", res.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&tokens); err != nil {
		return tokens, errors.New("invalid token response")
	}
	if tokens.AccessToken == "" || !strings.EqualFold(tokens.TokenType, "Bearer") || tokens.ExpiresIn <= 0 || tokens.ExpiresIn > 86400*365 {
		return tokens, errors.New("invalid token response")
	}
	return tokens, nil
}

func RandomSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

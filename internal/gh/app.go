package gh

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TokenSource yields a GitHub bearer token (PAT or installation token).
type TokenSource interface {
	Token() (string, error)
}

type StaticToken string

func (s StaticToken) Token() (string, error) {
	if s == "" {
		return "", fmt.Errorf("github: empty token")
	}
	return string(s), nil
}

// InstallationTokens mints GitHub App installation access tokens.
type InstallationTokens struct {
	AppID          int64
	InstallationID int64
	Key            *rsa.PrivateKey
	BaseURL        string
	HTTP           *http.Client
	Now            func() time.Time

	mu     sync.Mutex
	cached string
	expiry time.Time
}

func ParseAppPrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("github: no PEM block")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("github: private key: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("github: not an RSA key")
	}
	return rk, nil
}

func LoadAppFromEnv(getenv func(string) string) (*InstallationTokens, error) {
	idStr := strings.TrimSpace(getenv("RUSUI_GITHUB_APP_ID"))
	if idStr == "" {
		return nil, fmt.Errorf("github: app id unset")
	}
	appID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || appID <= 0 {
		return nil, fmt.Errorf("github: bad app id")
	}
	pemBytes := []byte(getenv("RUSUI_GITHUB_APP_PRIVATE_KEY"))
	if len(pemBytes) == 0 {
		path := getenv("RUSUI_GITHUB_APP_PRIVATE_KEY_FILE")
		if path == "" {
			return nil, fmt.Errorf("github: app private key unset")
		}
		pemBytes, err = os.ReadFile(path)
		if err != nil {
			return nil, err
		}
	}
	key, err := ParseAppPrivateKey(pemBytes)
	if err != nil {
		return nil, err
	}
	inst := InstallationTokens{AppID: appID, Key: key, BaseURL: githubAPI}
	if v := getenv("RUSUI_GITHUB_APP_INSTALLATION_ID"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("github: bad installation id")
		}
		inst.InstallationID = id
	}
	return &inst, nil
}

func (a *InstallationTokens) Token() (string, error) {
	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cached != "" && now.Add(time.Minute).Before(a.expiry) {
		return a.cached, nil
	}
	tok, exp, err := a.mint(now)
	if err != nil {
		return "", err
	}
	a.cached, a.expiry = tok, exp
	return tok, nil
}

func (a *InstallationTokens) mint(now time.Time) (string, time.Time, error) {
	jwt, err := appJWT(a.AppID, a.Key, now)
	if err != nil {
		return "", time.Time{}, err
	}
	base := a.BaseURL
	if base == "" {
		base = githubAPI
	}
	cli := a.HTTP
	if cli == nil {
		cli = http.DefaultClient
	}
	instID := a.InstallationID
	if instID == 0 {
		id, err := fetchInstallationID(cli, base, jwt)
		if err != nil {
			return "", time.Time{}, err
		}
		a.InstallationID = id
		instID = id
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(base, "/")+"/app/installations/"+strconv.FormatInt(instID, 10)+"/access_tokens", nil)
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "rusui")
	res, err := cli.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", time.Time{}, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", time.Time{}, fmt.Errorf("github: access_tokens: %s", res.Status)
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", time.Time{}, err
	}
	if out.Token == "" {
		return "", time.Time{}, fmt.Errorf("github: empty installation token")
	}
	if out.ExpiresAt.IsZero() {
		out.ExpiresAt = now.Add(time.Hour)
	}
	return out.Token, out.ExpiresAt, nil
}

func fetchInstallationID(cli *http.Client, base, jwt string) (int64, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(base, "/")+"/app/installations", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "rusui")
	res, err := cli.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return 0, fmt.Errorf("github: installations: %s", res.Status)
	}
	var list []struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return 0, err
	}
	if len(list) == 0 {
		return 0, fmt.Errorf("github: no installations")
	}
	return list[0].ID, nil
}

func appJWT(appID int64, key *rsa.PrivateKey, now time.Time) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	payload, _ := json.Marshal(map[string]any{
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": appID,
	})
	h := base64.RawURLEncoding.EncodeToString(header)
	p := base64.RawURLEncoding.EncodeToString(payload)
	sigInput := h + "." + p
	sum := sha256.Sum256([]byte(sigInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return sigInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

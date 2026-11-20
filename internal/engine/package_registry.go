package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

const (
	packageRegistrySecretID      = "package_registry"
	packageRegistryReceiptKind   = "package_registry"
	packageRegistryOverlayPrefix = "package_registry:"
	packageRegistryOverlayOff    = "0"
)

// PackageRegistryToken is a plane-minted identity for one package-registry host.
type PackageRegistryToken struct {
	Value  string
	Expiry time.Time
}

// PackageRegistryIssuer mints a token whose TTL is at most the per-turn grant.
// The durable issuer credential stays on the issuer, not in the guest.
type PackageRegistryIssuer interface {
	Mint(host string, ttl time.Duration, sessionID int64, project string, turnID int64) (PackageRegistryToken, error)
}

// HTTPPackageRegistryIssuer exchanges the plane-held credential at the
// named host's token endpoint. Endpoint, when set, overrides
// https://<host>/token (tests).
type HTTPPackageRegistryIssuer struct {
	Durable  string
	Client   *http.Client
	Endpoint string
}

type packageRegistryMintRequest struct {
	Audience   string `json:"audience"`
	TTLSeconds int    `json:"ttl_seconds"`
	Subject    string `json:"subject"`
	Turn       int64  `json:"turn"`
}

type packageRegistryMintResponse struct {
	Token     string `json:"token"`
	ExpiresIn int    `json:"expires_in"`
}

func (h HTTPPackageRegistryIssuer) Mint(host string, ttl time.Duration, sessionID int64, project string, turnID int64) (PackageRegistryToken, error) {
	if strings.TrimSpace(h.Durable) == "" {
		return PackageRegistryToken{}, fmt.Errorf("issuer unavailable")
	}
	if ttl <= 0 {
		return PackageRegistryToken{}, fmt.Errorf("ttl required")
	}
	body, err := json.Marshal(packageRegistryMintRequest{
		Audience:   host,
		TTLSeconds: int(ttl / time.Second),
		Subject:    packageRegistrySubject(sessionID, project),
		Turn:       turnID,
	})
	if err != nil {
		return PackageRegistryToken{}, err
	}
	req, err := http.NewRequest(http.MethodPost, h.endpoint(host), bytes.NewReader(body))
	if err != nil {
		return PackageRegistryToken{}, err
	}
	req.Header.Set("Authorization", "Bearer "+h.Durable)
	req.Header.Set("Content-Type", "application/json")
	res, err := h.client().Do(req)
	if err != nil {
		return PackageRegistryToken{}, fmt.Errorf("issuer unavailable")
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return PackageRegistryToken{}, fmt.Errorf("issuer unavailable")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return PackageRegistryToken{}, fmt.Errorf("issuer unavailable")
	}
	var out packageRegistryMintResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return PackageRegistryToken{}, fmt.Errorf("issuer unavailable")
	}
	if out.Token == "" || out.Token == h.Durable {
		return PackageRegistryToken{}, fmt.Errorf("registry cannot issue a shorter-lived token")
	}
	if out.ExpiresIn <= 0 || time.Duration(out.ExpiresIn)*time.Second > ttl {
		return PackageRegistryToken{}, fmt.Errorf("registry cannot issue a shorter-lived token")
	}
	return PackageRegistryToken{Value: out.Token, Expiry: time.Now().UTC().Add(time.Duration(out.ExpiresIn) * time.Second)}, nil
}

func (h HTTPPackageRegistryIssuer) endpoint(host string) string {
	if h.Endpoint != "" {
		return h.Endpoint
	}
	return "https://" + host + "/token"
}

func (h HTTPPackageRegistryIssuer) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func packageRegistrySubject(sessionID int64, project string) string {
	return fmt.Sprintf("session %d project %s", sessionID, project)
}

func packageRegistryOverlayKey(slug string) string {
	return packageRegistryOverlayPrefix + slug
}

func (e *Engine) effectivePackageRegistryHost(p policy.Project) (string, error) {
	if p.PackageRegistry == "" {
		return "", nil
	}
	var overlay string
	err := e.Store.Tx(func(tx *store.Tx) error {
		v, err := store.OverlayGet(tx, packageRegistryOverlayKey(p.Slug))
		overlay = v
		return err
	})
	if err != nil {
		return "", err
	}
	if overlay == packageRegistryOverlayOff {
		return "", nil
	}
	return p.PackageRegistry, nil
}

func (e *Engine) mintPackageRegistryForTurn(turnID int64) error {
	if turnID <= 0 {
		return nil
	}
	turn, err := store.GetTurn(e.Store, turnID)
	if err != nil {
		return err
	}
	sess, err := store.GetSession(e.Store, turn.SessionID)
	if err != nil {
		return err
	}
	if sess.EnvironmentID == store.DefaultEnvironmentID {
		return nil
	}
	envRow, err := store.GetEnvironment(e.Store, sess.EnvironmentID)
	if err != nil {
		return err
	}
	proj, ok := e.projectForSecretRepo(sess.Repo)
	if !ok && sess.Project != "" {
		proj, ok = e.PolicySnapshot().Project(sess.Project)
	}
	if !ok {
		return nil
	}
	host, err := e.effectivePackageRegistryHost(proj)
	if err != nil {
		return err
	}
	if host == "" {
		return nil
	}
	if envRow.Handle == "" {
		return fmt.Errorf("package-registry identity: guest unavailable")
	}
	if e.PackageRegistryIssuer == nil {
		return fmt.Errorf("package-registry identity: issuer unavailable")
	}
	ttl := GrantTTL
	tok, err := e.PackageRegistryIssuer.Mint(host, ttl, sess.ID, proj.Slug, turnID)
	if err != nil {
		return fmt.Errorf("package-registry identity: %w", err)
	}
	if tok.Value == "" {
		return fmt.Errorf("package-registry identity: issuer unavailable")
	}
	deadline := e.now().Add(ttl)
	if tok.Expiry.IsZero() || tok.Expiry.After(deadline.Add(time.Second)) {
		return fmt.Errorf("package-registry identity: registry cannot issue a shorter-lived token")
	}
	d, err := e.driverFor(envRow.Driver)
	if err != nil {
		return err
	}
	k, ok := d.(env.SecretKeeper)
	if !ok {
		return fmt.Errorf("package-registry identity: guest cannot hold secrets")
	}
	if err := k.PutSecret(envRow.Handle, packageRegistrySecretID, tok.Value); err != nil {
		return err
	}
	detail := fmt.Sprintf("audience %s host %s %s expires %s turn %d",
		packageRegistrySecretID, host, packageRegistrySubject(sess.ID, proj.Slug),
		tok.Expiry.UTC().Format(time.RFC3339), turnID)
	if strings.Contains(detail, tok.Value) {
		return fmt.Errorf("package-registry identity: token leaked into receipt")
	}
	return store.InsertEnvironmentReceipt(e.Store, envRow.ID, packageRegistryReceiptKind, "succeeded", detail, e.now())
}

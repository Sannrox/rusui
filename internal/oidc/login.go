package oidc

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Login launches the authorization URL through openURL and waits for a loopback callback.
// The caller owns browser launching and output; credentials are saved only on success.
func Login(ctx context.Context, server string, port int, planeClient *http.Client, openURL func(string) error) error {
	base, err := ServerURL(server)
	if err != nil {
		return err
	}
	client := HTTPClient()
	var meta Metadata
	if err = GetJSON(ctx, planeClient, base+"/auth/oidc", &meta); err != nil {
		return err
	}
	if meta.ClientID == "" || meta.Audience == "" {
		return errors.New("OIDC: incomplete server metadata")
	}
	d, err := Discover(ctx, client, meta.Issuer)
	if err != nil {
		return err
	}
	old, err := LoadCredentials(base)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && (old.Issuer != meta.Issuer || old.TokenEndpoint != d.TokenEndpoint || old.ClientID != meta.ClientID) {
		return errors.New("OIDC: saved issuer/client changed; logout before signing in again")
	}
	verifier, err := RandomSecret()
	if err != nil {
		return err
	}
	state, err := RandomSecret()
	if err != nil {
		return err
	}
	challenge := sha256.Sum256([]byte(verifier))
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	redirect := "http://" + listener.Addr().String() + "/callback"
	authURL, _ := url.Parse(d.AuthorizationEndpoint)
	q := authURL.Query()
	q.Set("response_type", "code")
	q.Set("client_id", meta.ClientID)
	q.Set("redirect_uri", redirect)
	q.Set("scope", strings.Join(meta.Scopes, " "))
	q.Set("state", state)
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	q.Set("code_challenge_method", "S256")
	authURL.RawQuery = q.Encode()
	type result struct {
		code string
		err  error
	}
	results := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		values := r.URL.Query()
		if r.Host != listener.Addr().String() || len(values["state"]) != 1 || subtle.ConstantTimeCompare([]byte(values.Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid login state", http.StatusBadRequest)
			return
		}
		response := result{code: values.Get("code")}
		if values.Get("error") != "" || len(values["code"]) != 1 || response.code == "" {
			response.err = errors.New("OIDC: authorization refused")
		}
		select {
		case results <- response:
		default:
			http.Error(w, "Callback already received", http.StatusConflict)
			return
		}
		_, _ = fmt.Fprintln(w, "Sign-in received. You can close this window.")
	})
	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer func() { _ = httpServer.Close() }()
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(listener) }()
	if err = openURL(authURL.String()); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("OIDC: login canceled or timed out: %w", ctx.Err())
	case err = <-serveErr:
		return err
	case res := <-results:
		if res.err != nil {
			return res.err
		}
		tokens, err := Exchange(ctx, client, d.TokenEndpoint, url.Values{"grant_type": {"authorization_code"}, "code": {res.code}, "client_id": {meta.ClientID}, "redirect_uri": {redirect}, "code_verifier": {verifier}})
		if err != nil {
			return err
		}
		// Prove the credential is accepted by this plane before retaining it locally.
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/auth/oidc/session", nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
		checked, err := planeClient.Do(req)
		if err != nil {
			return err
		}
		_ = checked.Body.Close()
		if checked.StatusCode != http.StatusNoContent {
			return errors.New("OIDC: plane rejected the access token")
		}
		return SaveCredentials(Credentials{Server: base, Issuer: meta.Issuer, TokenEndpoint: d.TokenEndpoint, ClientID: meta.ClientID, AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken, ExpiresAt: time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)})
	}
}

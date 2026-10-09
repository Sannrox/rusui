package oidc_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/oidc"
	"github.com/sannrox/rusui/internal/oidc/oidctest"
	"github.com/sannrox/rusui/internal/server"
)

func TestLoginCompletesPKCEAndRejectsWrongState(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p := oidctest.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	v, err := oidc.NewVerifier(ctx, oidc.Config{Metadata: oidc.Metadata{Issuer: p.Server.URL, Audience: "rusui", ClientID: "cli", Scopes: []string{"openid"}}, Subject: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	plane := httptest.NewServer((&server.Server{OIDC: v}).Handler())
	defer plane.Close()
	var authorization url.Values
	p.TokenHandler = func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			http.Error(w, "bad form", 400)
			return
		}
		challenge := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "code" || r.Form.Get("client_id") != "cli" || r.Form.Get("redirect_uri") != authorization.Get("redirect_uri") || base64.RawURLEncoding.EncodeToString(challenge[:]) != authorization.Get("code_challenge") {
			t.Error("PKCE token exchange did not bind authorization request")
			http.Error(w, "bad PKCE", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(oidc.Tokens{AccessToken: p.Token(t, p.Claims()), RefreshToken: "refresh", TokenType: "Bearer", ExpiresIn: 3600})
	}
	err = oidc.Login(ctx, plane.URL, 0, oidc.HTTPClient(), func(raw string) error {
		auth, err := url.Parse(raw)
		if err != nil {
			return err
		}
		authorization = auth.Query()
		if authorization.Get("code_challenge_method") != "S256" || authorization.Get("response_type") != "code" || authorization.Get("state") == "" {
			return fmt.Errorf("missing PKCE/state")
		}
		callback, err := url.Parse(authorization.Get("redirect_uri"))
		if err != nil {
			return err
		}
		q := url.Values{"state": {"wrong"}, "code": {"code"}}
		callback.RawQuery = q.Encode()
		response, err := http.Get(callback.String())
		if err != nil {
			return err
		}
		_ = response.Body.Close()
		if response.StatusCode != 400 {
			return fmt.Errorf("wrong state accepted")
		}
		q.Set("state", authorization.Get("state"))
		callback.RawQuery = q.Encode()
		response, err = http.Get(callback.String())
		if err != nil {
			return err
		}
		_ = response.Body.Close()
		if response.StatusCode != 200 {
			return fmt.Errorf("callback refused")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := oidc.LoadCredentials(plane.URL)
	if err != nil {
		t.Fatal(err)
	}
	if c.Issuer != p.Server.URL || c.TokenEndpoint != p.Server.URL+"/token" || c.RefreshToken != "refresh" {
		t.Fatalf("unpinned credential %+v", c)
	}
	if err := oidc.Logout(plane.URL); err != nil {
		t.Fatal(err)
	}
}

func TestLoginDoesNotSaveTokenRefusedByPlane(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p := oidctest.New(t)
	p.TokenHandler = func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(oidc.Tokens{AccessToken: "not-a-token", TokenType: "Bearer", ExpiresIn: 3600})
	}
	plane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/oidc" {
			_ = json.NewEncoder(w).Encode(oidc.Metadata{Issuer: p.Server.URL, Audience: "rusui", ClientID: "cli", Scopes: []string{"openid"}})
			return
		}
		w.WriteHeader(401)
	}))
	defer plane.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := oidc.Login(ctx, plane.URL, 0, oidc.HTTPClient(), func(raw string) error {
		u, _ := url.Parse(raw)
		q := u.Query()
		res, err := http.Get(q.Get("redirect_uri") + "?" + url.Values{"state": {q.Get("state")}, "code": {"code"}}.Encode())
		if err == nil {
			_ = res.Body.Close()
		}
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("refused token error=%v", err)
	}
	if _, err := oidc.LoadCredentials(plane.URL); err == nil {
		t.Fatal("refused token saved")
	}
}

func TestLoginTimeoutAndPinnedIssuerChange(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p := oidctest.New(t)
	plane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(oidc.Metadata{Issuer: p.Server.URL, Audience: "rusui", ClientID: "cli", Scopes: []string{"openid"}})
	}))
	defer plane.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := oidc.Login(ctx, plane.URL, 0, oidc.HTTPClient(), func(string) error { return nil }); err == nil {
		t.Fatal("login ignored timeout")
	}
	if err := oidc.SaveCredentials(oidc.Credentials{Server: plane.URL, Issuer: "https://old.example", TokenEndpoint: "https://old.example/token", ClientID: "cli", AccessToken: "old", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := oidc.Login(context.Background(), plane.URL, 0, oidc.HTTPClient(), func(string) error { t.Error("opened browser after issuer changed"); return nil }); err == nil {
		t.Fatal("silently repinned issuer")
	}
}

func TestTokenExchangeRejectsCredentialRedirect(t *testing.T) {
	leaked := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true; _, _ = io.WriteString(w, "{}") }))
	defer target.Close()
	origin := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusTemporaryRedirect))
	defer origin.Close()
	if _, err := oidc.Exchange(context.Background(), oidc.HTTPClient(), origin.URL, url.Values{"refresh_token": {"secret"}}); err == nil || leaked {
		t.Fatal("token request followed redirect")
	}
}

package oidc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCredentialsArePrivatePerServerAndRefreshPinnedEndpoint(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	var refresh int
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refresh++
		_ = json.NewEncoder(w).Encode(Tokens{AccessToken: "new", RefreshToken: "rotated", TokenType: "Bearer", ExpiresIn: 3600})
	}))
	defer tokenServer.Close()
	c := Credentials{Server: "https://plane.example", Issuer: "https://issuer.example", TokenEndpoint: tokenServer.URL, ClientID: "cli", AccessToken: "old", RefreshToken: "refresh", ExpiresAt: time.Now().Add(-time.Minute)}
	if err := SaveCredentials(c); err != nil {
		t.Fatal(err)
	}
	path, err := credentialPath("https://plane.example")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("permissions %o", info.Mode().Perm())
	}
	got, err := SavedToken(context.Background(), "https://plane.example")
	if err != nil {
		t.Fatal(err)
	}
	if got != "new" || refresh != 1 {
		t.Fatalf("token=%q refresh=%d", got, refresh)
	}
	other := Credentials{Server: "https://other.example", Issuer: c.Issuer, TokenEndpoint: tokenServer.URL, ClientID: c.ClientID, AccessToken: "other", ExpiresAt: time.Now().Add(time.Hour)}
	if err := SaveCredentials(other); err != nil {
		t.Fatal(err)
	}
	got, err = SavedToken(context.Background(), "https://plane.example")
	if err != nil || got != "new" {
		t.Fatalf("cross-server read token=%q err=%v", got, err)
	}
	if err := Logout("https://plane.example"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredentials("https://plane.example"); !os.IsNotExist(err) {
		t.Fatalf("logout error %v", err)
	}
}

func TestCredentialsRejectInsecureOrPublicFiles(t *testing.T) {
	if _, err := ServerURL("https://plane.example?x=1"); err == nil {
		t.Fatal("query accepted")
	}
	if _, err := URL("http://example.com"); err == nil {
		t.Fatal("public HTTP accepted")
	}
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	c := Credentials{Server: "https://plane.example", Issuer: "https://issuer.example", TokenEndpoint: "https://issuer.example/token", ClientID: "cli", AccessToken: "x", ExpiresAt: time.Now().Add(time.Hour)}
	if err := SaveCredentials(c); err != nil {
		t.Fatal(err)
	}
	path, _ := credentialPath(c.Server)
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredentials(c.Server); err == nil {
		t.Fatal("world-readable credentials accepted")
	}
	if _, err := LoadCredentials(filepath.Join(config, "missing")); err == nil {
		t.Fatal("invalid server accepted")
	}
}

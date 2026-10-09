package oidc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscoverRejectsRedirectsAndIssuerMismatch(t *testing.T) {
	redirect := httptest.NewServer(http.RedirectHandler("https://example.com", http.StatusFound))
	defer redirect.Close()
	if _, err := Discover(context.Background(), HTTPClient(), redirect.URL); err == nil {
		t.Fatal("redirect accepted")
	}
	mismatch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Discovery{Issuer: "https://other", AuthorizationEndpoint: redirect.URL, TokenEndpoint: redirect.URL, JWKSURI: redirect.URL})
	}))
	defer mismatch.Close()
	if _, err := Discover(context.Background(), HTTPClient(), mismatch.URL); err == nil {
		t.Fatal("issuer mismatch accepted")
	}
}

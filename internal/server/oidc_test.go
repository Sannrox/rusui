package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/oidc"
	"github.com/sannrox/rusui/internal/oidc/oidctest"
)

func TestOIDCSeparatesOperatorAPIFromBrowserAndWorker(t *testing.T) {
	p := oidctest.New(t)
	ctx := t.Context()
	verifier, err := oidc.NewVerifier(ctx, oidc.Config{Metadata: oidc.Metadata{Issuer: p.Server.URL, Audience: "rusui", ClientID: "cli", Scopes: []string{"openid"}}, Subject: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{OperatorTok: "op", WorkerSec: "worker", OIDC: verifier}
	raw := p.Token(t, p.Claims())
	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/session", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	if !s.OperatorAPIOK(req) || !s.operatorOrWorkerOK(req) || s.OperatorBrowserOK(req) || s.workerOK(req) {
		t.Fatal("credential classes mixed")
	}
	for _, tc := range []struct {
		method, path, token string
		want                int
	}{
		{"GET", "/auth/oidc/session", raw, 204},
		{"GET", "/auth/oidc/session", "worker", 401},
		{"GET", "/auth/oidc/session", "", 401},
		{"POST", "/jobs/claim", raw, 401},
		{"POST", "/runners/hello", raw, 401},
		{"GET", "/console/sessions", raw, 303},
	} {
		request := httptest.NewRequest(tc.method, tc.path, nil)
		request.Host = "127.0.0.1"
		request.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, request)
		if w.Code != tc.want {
			t.Fatalf("%s %s = %d, want %d", tc.method, tc.path, w.Code, tc.want)
		}
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/auth/oidc", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), "subject") || strings.Contains(w.Body.String(), "operator") {
		t.Fatalf("public metadata %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	(&Server{}).Handler().ServeHTTP(w, httptest.NewRequest("GET", "/auth/oidc", nil))
	if w.Code != 404 {
		t.Fatal("disabled OIDC advertised")
	}
}

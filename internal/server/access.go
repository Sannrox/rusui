package server

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/sannrox/rusui/internal/store"
)

type principalCtxKey struct{}

// CredentialClass is the plane-facing identity of a presented secret.
type CredentialClass int

const (
	CredNone CredentialClass = iota
	CredOperator
	CredWorker
	CredTurn
)

// ClassifyBearer maps a presented bearer to a credential class.
// Worker and operator secrets are never interchangeable.
func ClassifyBearer(tok, operator, worker string) CredentialClass {
	if tok == "" {
		return CredNone
	}
	if operator != "" && worker != "" && operator == worker {
		return CredNone
	}
	if operator != "" && tok == operator {
		return CredOperator
	}
	if worker != "" && tok == worker {
		return CredWorker
	}
	return CredNone
}

func bearerToken(r *http.Request) string {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return tok
}

// Principal is the authenticated caller and, for operator credentials,
// the tenant isolation key they may observe.
type Principal struct {
	Class    CredentialClass
	TenantID string
}

// OperatorBrowserOK is true only for the operator class. Worker and turn
// secrets cannot mint a console session.
func (s *Server) OperatorBrowserOK(r *http.Request) bool {
	return ClassifyBearer(bearerToken(r), s.OperatorTok, s.WorkerSec) == CredOperator
}

// OperatorAPIOK permits static, per-tenant, or OIDC credentials in the
// operator class. OIDC credentials never pass the browser, worker, or turn
// checks. Tenant tokens authenticate the operator class for their tenant.
func (s *Server) OperatorAPIOK(r *http.Request) bool {
	p := s.identify(r)
	return p.Class == CredOperator
}

// operatorOrWorkerOK is the session/approval observer path: operator
// console/editor and the worker CLI share it. Claim/complete stay worker-only.
func (s *Server) operatorOrWorkerOK(r *http.Request) bool {
	p := s.identify(r)
	if p.Class == CredOperator || p.Class == CredWorker {
		return true
	}
	// WorkerOK also accepts X-Worker-Token; keep that for CLI.
	return s.workerOK(r)
}

func (s *Server) withPrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := s.lookupPrincipal(r)
		if p.Class == CredNone && s.workerOK(r) {
			p = Principal{Class: CredWorker}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalCtxKey{}, p)))
	})
}

func (s *Server) identify(r *http.Request) Principal {
	if p, ok := r.Context().Value(principalCtxKey{}).(Principal); ok {
		return p
	}
	p := s.lookupPrincipal(r)
	if p.Class == CredNone && s.workerOK(r) {
		return Principal{Class: CredWorker}
	}
	return p
}

func (s *Server) lookupPrincipal(r *http.Request) Principal {
	tok := bearerToken(r)
	if tenant := s.tenantForToken(tok); tenant != "" {
		return Principal{Class: CredOperator, TenantID: tenant}
	}
	switch ClassifyBearer(tok, s.OperatorTok, s.WorkerSec) {
	case CredOperator:
		return Principal{Class: CredOperator, TenantID: store.DefaultTenantID}
	case CredWorker:
		return Principal{Class: CredWorker}
	}
	if tok == "" || tok == s.WorkerSec || (s.OperatorTok != "" && s.OperatorTok == s.WorkerSec) {
		return Principal{Class: CredNone}
	}
	if s.OIDC != nil && s.OIDC.Valid(tok) {
		return Principal{Class: CredOperator, TenantID: store.DefaultTenantID}
	}
	return Principal{Class: CredNone}
}

func (s *Server) tenantForToken(tok string) string {
	if tok == "" || len(s.TenantToks) == 0 {
		return ""
	}
	found := ""
	matches := 0
	for tenant, secret := range s.TenantToks {
		if secret == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(tok), []byte(secret)) == 1 {
			found = tenant
			matches++
		}
	}
	if matches != 1 {
		return ""
	}
	return found
}

func (s *Server) operatorTenant(r *http.Request) (string, bool) {
	p := s.identify(r)
	if p.Class != CredOperator {
		return "", false
	}
	return p.TenantID, true
}

func (s *Server) sessionAllowed(r *http.Request, sess *store.Session) bool {
	switch p := s.identify(r); p.Class {
	case CredOperator:
		return store.NormalizeTenantID(sess.TenantID) == p.TenantID
	case CredWorker:
		return true
	}
	if s.consoleAuthed(r) {
		return store.NormalizeTenantID(sess.TenantID) == store.DefaultTenantID
	}
	return false
}

func (s *Server) repoAllowed(r *http.Request, repo string) bool {
	switch p := s.identify(r); p.Class {
	case CredWorker:
		return true
	case CredOperator:
		proj, found := s.Eng.PolicySnapshot().ProjectForRepo(repo)
		if !found {
			return false
		}
		return store.NormalizeTenantID(proj.Tenant) == p.TenantID
	}
	return false
}

func (s *Server) projectAllowed(r *http.Request, slug string) bool {
	switch p := s.identify(r); p.Class {
	case CredWorker:
		return true
	case CredOperator:
		proj, found := s.Eng.PolicySnapshot().Project(slug)
		if !found {
			return false
		}
		return store.NormalizeTenantID(proj.Tenant) == p.TenantID
	}
	return false
}

func (s *Server) reviewSessionAllowed(r *http.Request, repo string, item int) bool {
	p := s.identify(r)
	if p.Class != CredOperator {
		return p.Class == CredWorker
	}
	sess, err := store.ReviewSession(s.Eng.Store, repo, item)
	if err != nil {
		return false
	}
	if sess == nil {
		return true
	}
	return store.NormalizeTenantID(sess.TenantID) == p.TenantID
}

func (s *Server) visibleSession(r *http.Request, id int64) (*store.Session, error) {
	sess, err := store.GetSession(s.Eng.Store, id)
	if err != nil {
		return nil, err
	}
	if !s.sessionAllowed(r, sess) {
		return nil, sql.ErrNoRows
	}
	return sess, nil
}

func (s *Server) visibleApprovalAction(r *http.Request, id string) (*store.Action, error) {
	act, err := store.GetAction(s.Eng.Store, id)
	if err != nil {
		return nil, err
	}
	tenant, ok := s.operatorTenant(r)
	if !ok {
		return act, nil
	}
	if act.SessionID == nil {
		return nil, sql.ErrNoRows
	}
	sess, err := store.GetSession(s.Eng.Store, *act.SessionID)
	if err != nil {
		return nil, err
	}
	if store.NormalizeTenantID(sess.TenantID) != tenant {
		return nil, sql.ErrNoRows
	}
	return act, nil
}

// LoadTenantTokens parses RUSUI_TENANT_TOKENS: a JSON object of tenant_id
// to operator token, or a path to such a file. The default tenant is the
// static RUSUI_OPERATOR_TOKEN and must not appear here.
func LoadTenantTokens(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	body := []byte(raw)
	if !strings.HasPrefix(raw, "{") {
		b, err := os.ReadFile(raw)
		if err != nil {
			return nil, fmt.Errorf("tenant tokens: %w", err)
		}
		body = b
	}
	var m map[string]string
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("tenant tokens: %w", err)
	}
	out := make(map[string]string, len(m))
	seen := map[string]string{}
	for tenant, tok := range m {
		tenant = strings.TrimSpace(tenant)
		tok = strings.TrimSpace(tok)
		if tenant == "" || tok == "" {
			return nil, fmt.Errorf("tenant tokens: empty tenant or token")
		}
		if tenant == store.DefaultTenantID {
			return nil, fmt.Errorf("tenant tokens: default tenant uses RUSUI_OPERATOR_TOKEN")
		}
		if err := validTenantID(tenant); err != nil {
			return nil, err
		}
		if other, ok := seen[tok]; ok {
			return nil, fmt.Errorf("tenant tokens: %s and %s share a token", other, tenant)
		}
		seen[tok] = tenant
		out[tenant] = tok
	}
	return out, nil
}

func validTenantID(id string) error {
	for i, c := range id {
		ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || (c == '-' && i > 0)
		if !ok {
			return fmt.Errorf("tenant tokens: invalid tenant %q", id)
		}
	}
	if id[0] < 'a' || id[0] > 'z' {
		return fmt.Errorf("tenant tokens: invalid tenant %q", id)
	}
	return nil
}

func (s *Server) CheckTenantTokenOverlap() error {
	seen := map[string]string{}
	for tenant, tok := range s.TenantToks {
		if s.OperatorTok != "" && tok == s.OperatorTok {
			return fmt.Errorf("tenant tokens: %s collides with operator token", tenant)
		}
		if s.WorkerSec != "" && tok == s.WorkerSec {
			return fmt.Errorf("tenant tokens: %s collides with worker secret", tenant)
		}
		if other, ok := seen[tok]; ok {
			return fmt.Errorf("tenant tokens: %s and %s share a token", other, tenant)
		}
		seen[tok] = tenant
	}
	return nil
}

// PreviewOriginIsolated reports whether previewHost is a different origin
// from the plane. Same host (including portless default) fails closed.
func PreviewOriginIsolated(planeURL, previewURL string) bool {
	p, err1 := url.Parse(planeURL)
	v, err2 := url.Parse(previewURL)
	if err1 != nil || err2 != nil {
		return false
	}
	if p.Scheme == "" || v.Scheme == "" || p.Host == "" || v.Host == "" {
		return false
	}
	return !strings.EqualFold(p.Scheme, v.Scheme) || !strings.EqualFold(p.Host, v.Host)
}

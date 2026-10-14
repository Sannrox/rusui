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
	Actor    string
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
		return Principal{Class: CredOperator, TenantID: tenant, Actor: s.actorForToken(tok, tenant)}
	}
	switch ClassifyBearer(tok, s.OperatorTok, s.WorkerSec) {
	case CredOperator:
		return Principal{Class: CredOperator, TenantID: store.DefaultTenantID, Actor: store.DefaultTenantID}
	case CredWorker:
		return Principal{Class: CredWorker}
	}
	if tok == "" || tok == s.WorkerSec || (s.OperatorTok != "" && s.OperatorTok == s.WorkerSec) {
		return Principal{Class: CredNone}
	}
	if s.OIDC != nil && s.OIDC.Valid(tok) {
		return Principal{Class: CredOperator, TenantID: store.DefaultTenantID, Actor: store.DefaultTenantID}
	}
	return Principal{Class: CredNone}
}

func (s *Server) tenantForToken(tok string) string {
	if tok == "" {
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
	if matches == 1 {
		return found
	}
	if matches > 1 {
		return ""
	}
	if t := s.MemberToks[tok]; t != "" {
		return t
	}
	return ""
}

func (s *Server) actorForToken(tok, tenant string) string {
	if a := s.ActorToks[tok]; a != "" {
		return a
	}
	return tenant
}

func (s *Server) operatorActor(p Principal) string {
	if p.Actor != "" {
		return p.Actor
	}
	return p.TenantID
}

func userMode(sess *store.Session) bool {
	return store.NormalizeExecutionMode(sess.ExecutionMode) == store.ExecutionUser
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
		if store.NormalizeTenantID(sess.TenantID) != p.TenantID {
			if userMode(sess) {
				return false
			}
			ok, err := store.SessionGranted(s.Eng.Store, sess.ID, p.TenantID)
			return err == nil && ok
		}
		if userMode(sess) {
			return store.NormalizeActor(sess.Act) == store.NormalizeActor(s.operatorActor(p))
		}
		return true
	case CredWorker:
		return true
	}
	if s.consoleAuthed(r) {
		if store.NormalizeTenantID(sess.TenantID) != store.DefaultTenantID {
			if userMode(sess) {
				return false
			}
			ok, err := store.SessionGranted(s.Eng.Store, sess.ID, store.DefaultTenantID)
			return err == nil && ok
		}
		if userMode(sess) {
			return store.NormalizeActor(sess.Act) == store.DefaultTenantID
		}
		return true
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

func (s *Server) visibleOwnedSession(r *http.Request, id int64) (*store.Session, error) {
	sess, err := store.GetSession(s.Eng.Store, id)
	if err != nil {
		return nil, err
	}
	switch s.identify(r).Class {
	case CredWorker:
		return sess, nil
	case CredOperator:
		if !s.sessionOwner(r, sess) {
			return nil, sql.ErrNoRows
		}
		if userMode(sess) && store.NormalizeActor(sess.Act) != store.NormalizeActor(s.operatorActor(s.identify(r))) {
			return nil, sql.ErrNoRows
		}
		return sess, nil
	}
	if s.consoleAuthed(r) && store.NormalizeTenantID(sess.TenantID) == store.DefaultTenantID {
		if userMode(sess) && store.NormalizeActor(sess.Act) != store.DefaultTenantID {
			return nil, sql.ErrNoRows
		}
		return sess, nil
	}
	return nil, sql.ErrNoRows
}

func (s *Server) sessionStillAllowed(r *http.Request, id int64) bool {
	_, err := s.visibleSession(r, id)
	return err == nil
}

func (s *Server) visibleApprovalAction(r *http.Request, id string) (*store.Action, error) {
	act, err := store.GetAction(s.Eng.Store, id)
	if err != nil {
		return nil, err
	}
	if !s.actionSessionVisible(r, act) {
		return nil, sql.ErrNoRows
	}
	return act, nil
}

func (s *Server) actionSessionVisible(r *http.Request, act *store.Action) bool {
	if act == nil {
		return false
	}
	if s.identify(r).Class == CredWorker {
		return true
	}
	if act.SessionID == nil {
		return false
	}
	sess, err := store.GetSession(s.Eng.Store, *act.SessionID)
	if err != nil {
		return false
	}
	if s.consoleAuthed(r) && s.identify(r).Class != CredOperator {
		if store.NormalizeTenantID(sess.TenantID) != store.DefaultTenantID {
			return false
		}
		if userMode(sess) {
			return store.NormalizeActor(sess.Act) == store.DefaultTenantID
		}
		return true
	}
	tenant, ok := s.operatorTenant(r)
	if !ok {
		return false
	}
	if store.NormalizeTenantID(sess.TenantID) != tenant {
		return false
	}
	if userMode(sess) {
		return store.NormalizeActor(sess.Act) == store.NormalizeActor(s.operatorActor(s.identify(r)))
	}
	return true
}

// LoadTenantTokens parses RUSUI_TENANT_TOKENS: a JSON object of tenant_id
// to an operator bearer, or of tenant_id to a map of actor id to bearer,
// or a path to that file. The default tenant is the static
// RUSUI_OPERATOR_TOKEN and must not appear here.
func LoadTenantTokens(raw string) (tokens, members, actors map[string]string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil, nil, nil
	}
	body := []byte(raw)
	if !strings.HasPrefix(raw, "{") {
		b, err := os.ReadFile(raw)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("tenant tokens: %w", err)
		}
		body = b
	}
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(body, &rawMap); err != nil {
		return nil, nil, nil, fmt.Errorf("tenant tokens: %w", err)
	}
	tokens, members, actors = map[string]string{}, map[string]string{}, map[string]string{}
	seen := map[string]string{}
	add := func(tenant, actor, tok string) error {
		tenant, actor, tok = strings.TrimSpace(tenant), strings.TrimSpace(actor), strings.TrimSpace(tok)
		if tenant == "" || actor == "" || tok == "" {
			return fmt.Errorf("tenant tokens: empty tenant, actor, or token")
		}
		if tenant == store.DefaultTenantID {
			return fmt.Errorf("tenant tokens: default tenant uses RUSUI_OPERATOR_TOKEN")
		}
		if err := validTenantID(tenant); err != nil {
			return err
		}
		if err := validTenantID(actor); err != nil {
			return fmt.Errorf("tenant tokens: invalid actor %q", actor)
		}
		if other, ok := seen[tok]; ok {
			return fmt.Errorf("tenant tokens: %s and %s share a token", other, tenant+"/"+actor)
		}
		seen[tok] = tenant + "/" + actor
		actors[tok] = actor
		return nil
	}
	for tenant, val := range rawMap {
		var tok string
		if json.Unmarshal(val, &tok) == nil {
			if err := add(tenant, tenant, tok); err != nil {
				return nil, nil, nil, err
			}
			tokens[strings.TrimSpace(tenant)] = strings.TrimSpace(tok)
			continue
		}
		var inner map[string]string
		if err := json.Unmarshal(val, &inner); err != nil {
			return nil, nil, nil, fmt.Errorf("tenant tokens: %w", err)
		}
		if len(inner) == 0 {
			return nil, nil, nil, fmt.Errorf("tenant tokens: empty tenant %q", tenant)
		}
		for actor, tok := range inner {
			if err := add(tenant, actor, tok); err != nil {
				return nil, nil, nil, err
			}
			members[strings.TrimSpace(tok)] = strings.TrimSpace(tenant)
		}
	}
	return tokens, members, actors, nil
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
	check := func(label, tok string) error {
		if tok == "" {
			return nil
		}
		if s.OperatorTok != "" && tok == s.OperatorTok {
			return fmt.Errorf("tenant tokens: %s collides with operator token", label)
		}
		if s.WorkerSec != "" && tok == s.WorkerSec {
			return fmt.Errorf("tenant tokens: %s collides with worker secret", label)
		}
		if other, ok := seen[tok]; ok {
			return fmt.Errorf("tenant tokens: %s and %s share a token", other, label)
		}
		seen[tok] = label
		return nil
	}
	for tenant, tok := range s.TenantToks {
		if err := check(tenant, tok); err != nil {
			return err
		}
	}
	for tok, tenant := range s.MemberToks {
		actor := s.ActorToks[tok]
		if actor == "" {
			actor = tenant
		}
		if err := check(tenant+"/"+actor, tok); err != nil {
			return err
		}
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

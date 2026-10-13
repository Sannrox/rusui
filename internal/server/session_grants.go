package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/sannrox/rusui/internal/store"
)

func (s *Server) sessionOwner(r *http.Request, sess *store.Session) bool {
	tenant, ok := s.operatorTenant(r)
	if !ok {
		return false
	}
	return store.NormalizeTenantID(sess.TenantID) == tenant
}

func (s *Server) listSessionGrants(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorAPIOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	sess, err := store.GetSession(s.Eng.Store, id)
	if err != nil || sess == nil || !s.sessionOwner(r, sess) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	list, err := store.ListSessionAccessGrants(s.Eng.Store, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

func (s *Server) grantSessionAccess(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorAPIOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	sess, err := store.GetSession(s.Eng.Store, id)
	if err != nil || sess == nil || !s.sessionOwner(r, sess) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var req struct {
		Tenant string `json:"tenant"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		http.Error(w, "body", http.StatusBadRequest)
		return
	}
	if req.Tenant == "" || validTenantID(req.Tenant) != nil {
		http.Error(w, "tenant", http.StatusBadRequest)
		return
	}
	tenant, _ := s.operatorTenant(r)
	g, err := store.GrantSessionAccess(s.Eng.Store, id, req.Tenant, tenant)
	switch {
	case errors.Is(err, store.ErrGrantSelf), errors.Is(err, store.ErrInvalidGrantee):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case errors.Is(err, store.ErrGrantExists):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(g)
}

func (s *Server) revokeSessionAccess(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorAPIOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	sess, err := store.GetSession(s.Eng.Store, id)
	if err != nil || sess == nil || !s.sessionOwner(r, sess) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := store.RevokeSessionAccess(s.Eng.Store, id, r.PathValue("tenant")); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

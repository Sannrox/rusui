package server

import (
	"encoding/json"
	"net/http"
)

func (s *Server) oidcMetadata(w http.ResponseWriter, r *http.Request) {
	if s.OIDC == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.OIDC.Metadata())
}

func (s *Server) oidcSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.OperatorAPIOK(r) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

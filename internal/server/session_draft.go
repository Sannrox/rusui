package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/sannrox/rusui/internal/engine"
)

func (s *Server) applySessionDraft(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorAPIOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	if _, err := s.visibleOwnedSession(r, id); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	rec, err := s.Eng.ApplySessionDraft(id)
	switch {
	case errors.Is(err, engine.ErrDraftNoProposal), errors.Is(err, engine.ErrDraftCoreOff):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rec)
}

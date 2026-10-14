package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
)

func (s *Server) applySessionWrite(w http.ResponseWriter, r *http.Request) {
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
	rec, err := s.Eng.ApplySessionWrite(id)
	switch {
	case errors.Is(err, engine.ErrWriteNoProposal), errors.Is(err, engine.ErrWritePublisherOff),
		errors.Is(err, engine.ErrWriteNotImplement), errors.Is(err, gh.ErrHeadMismatch):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rec)
}

package server

import (
	"encoding/json"
	"net/http"
)

func (s *Server) drain(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	rep, err := s.Eng.Drain()
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rep)
}

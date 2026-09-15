package server

import (
	"encoding/json"
	"net/http"
)

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	var req struct {
		Kind   string `json:"kind"`
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Kind != "run" {
		http.Error(w, "kind", http.StatusBadRequest)
		return
	}
	id, err := s.Eng.StartRun(r.PathValue("slug"), req.Prompt, r.Header.Get("Idempotency-Key"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"session_id": id, "kind": "run"})
}

func (s *Server) createSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	var req struct {
		Name   string `json:"name"`
		Every  string `json:"every"`
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := s.Eng.CreateSchedule(r.PathValue("slug"), req.Name, req.Every, req.Prompt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "name": req.Name, "every": req.Every})
}

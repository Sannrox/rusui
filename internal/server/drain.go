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

// resume lifts the pause drain set, globally or for ?project=, the same
// as the Slack resume command.
func (s *Server) resume(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	project := r.URL.Query().Get("project")
	if err := s.Eng.SetPause(project, false); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"paused": false, "project": project})
}

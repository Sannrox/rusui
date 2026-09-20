package server

import (
	"encoding/json"
	"net/http"

	"github.com/sannrox/rusui/internal/engine"
)

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	var req struct {
		Kind         string   `json:"kind"`
		Prompt       string   `json:"prompt"`
		EffortKey    string   `json:"effort_key"`
		Repo         string   `json:"repo"`
		Ref          string   `json:"ref"`
		BaseSHA      string   `json:"base_sha"`
		AllowedPaths []string `json:"allowed_paths"`
		ContextRefs  []string `json:"context_refs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Kind != "run" {
		http.Error(w, "kind", http.StatusBadRequest)
		return
	}
	pinned := req.EffortKey != "" || req.Repo != "" || req.Ref != "" || req.BaseSHA != "" || len(req.AllowedPaths) > 0
	if pinned {
		task, err := s.Eng.StartTask(r.PathValue("slug"), engine.TaskSpec{
			EffortKey: req.EffortKey, Prompt: req.Prompt, Repo: req.Repo, Ref: req.Ref,
			BaseSHA: req.BaseSHA, AllowedPaths: req.AllowedPaths, ContextRefs: req.ContextRefs,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"session_id": task.SessionID, "kind": "run", "task_id": task.ID, "revision": task.Revision,
		})
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

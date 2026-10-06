package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

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
		Size         string   `json:"size"`
		EffortKey    string   `json:"effort_key"`
		Repo         string   `json:"repo"`
		Ref          string   `json:"ref"`
		BaseSHA      string   `json:"base_sha"`
		AllowedPaths []string `json:"allowed_paths"`
		ContextRefs  []string `json:"context_refs"`
		Argv         []string `json:"argv"`
		Cwd          string   `json:"cwd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Kind == "local" {
		if req.Prompt != "" || req.EffortKey != "" || req.Repo != "" || req.Ref != "" || req.BaseSHA != "" || len(req.AllowedPaths) > 0 || len(req.ContextRefs) > 0 {
			http.Error(w, "local session uses its policy-configured runtime", http.StatusBadRequest)
			return
		}
		if len(req.Argv) > 0 || req.Cwd != "" {
			http.Error(w, "local argv and cwd are policy-configured", http.StatusBadRequest)
			return
		}
		result, startErr := s.Eng.StartLocalSession(r.PathValue("slug"), r.Header.Get("Idempotency-Key"))
		if result.Session == nil {
			if startErr == nil {
				startErr = fmt.Errorf("local session was not created")
			}
			http.Error(w, startErr.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		response := map[string]any{"session_id": result.Session.ID, "kind": "local", "session": result.Session, "process": result.Process}
		if startErr != nil {
			response["start_error"] = startErr.Error()
		}
		_ = json.NewEncoder(w).Encode(response)
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
	id, err := s.Eng.StartRunSize(r.PathValue("slug"), req.Prompt, r.Header.Get("Idempotency-Key"), req.Size)
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
		Name      string `json:"name"`
		Every     string `json:"every"`
		Prompt    string `json:"prompt"`
		SessionID int64  `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := s.Eng.CreateSchedule(r.PathValue("slug"), req.Name, req.Every, req.Prompt, req.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	out := map[string]any{"id": id, "name": req.Name, "every": req.Every}
	if req.SessionID != 0 {
		out["session_id"] = req.SessionID
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	if err := s.Eng.DeleteSchedule(r.PathValue("slug"), id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

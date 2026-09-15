package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/sannrox/rusui/internal/store"
)

func (s *Server) sessionLogs(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", 400)
		return
	}
	if _, err := store.GetSession(s.Eng.Store, id); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	acts, err := store.ListActionsForSession(s.Eng.Store, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if acts == nil {
		acts = []store.Action{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(acts)
}

func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	list, err := store.ListPendingApprovals(s.Eng.Store)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if list == nil {
		list = []store.Action{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

func (s *Server) decideApproval(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	var req struct {
		Decision string `json:"decision"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Decision != "allow" && req.Decision != "deny" {
		http.Error(w, "decision", http.StatusBadRequest)
		return
	}
	if err := store.PutApprovalDecision(s.Eng.Store, r.PathValue("id"), req.Decision); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

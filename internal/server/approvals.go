package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/store"
)

func (s *Server) sessionLogs(w http.ResponseWriter, r *http.Request) {
	if !s.operatorOrWorkerOK(r) {
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
	if !s.operatorOrWorkerOK(r) {
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

func (s *Server) getApproval(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.operatorOrWorkerOK(r) {
		act, err := store.GetAction(s.Eng.Store, id)
		if err != nil || act.TurnID == nil || !s.turnOK(r, *act.TurnID) {
			http.Error(w, "auth", http.StatusUnauthorized)
			return
		}
	}
	decision, ok, err := store.GetApprovalDecision(s.Eng.Store, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	valid := false
	if ok && decision == "allow" {
		valid = s.allowStillValid(id)
	}
	if ok && decision == "deny" {
		valid = true
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"decision": decision, "valid": valid})
}

func (s *Server) allowStillValid(actionID string) bool {
	act, err := store.GetAction(s.Eng.Store, actionID)
	if err != nil || act.TurnID == nil {
		return false
	}
	turn, err := store.GetTurn(s.Eng.Store, *act.TurnID)
	if err != nil || turn.State != "leased" {
		return false
	}
	sess, err := store.GetSession(s.Eng.Store, turn.SessionID)
	if err != nil {
		return false
	}
	var paused bool
	_ = s.Eng.Store.Tx(func(tx *sql.Tx) error {
		paused, err = store.Paused(tx, sess.Project)
		return err
	})
	if paused {
		return false
	}
	var params acp.PermissionParams
	_ = json.Unmarshal([]byte(act.Body), &params)
	if s.agentGitHubToken(sess) != "" {
		// Implement sessions: the built-in fence overrides any approval.
		if d := (acp.FenceGate{Next: acp.RulesGate{}}).Decide(params); d.Matched && !d.Allow {
			return false
		}
	}
	if p, ok := s.Eng.PolicySnapshot().Project(sess.Project); ok {
		rules := make([]acp.Rule, 0, len(p.Permissions))
		for _, r := range p.Permissions {
			rules = append(rules, acp.Rule{Tool: r.Tool, Kind: r.Kind, Command: r.Command, Action: r.Action})
		}
		d := acp.RulesGate{Rules: rules}.Decide(params)
		if d.Matched && !d.Allow {
			return false
		}
	}
	return true
}

func (s *Server) decideApproval(w http.ResponseWriter, r *http.Request) {
	if !s.operatorOrWorkerOK(r) {
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

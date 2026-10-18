package server

import (
	"encoding/json"
	"net/http"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/store"
)

type haltReq struct {
	Scope    string `json:"scope"`
	Target   string `json:"target"`
	Reason   string `json:"reason"`
	Operator string `json:"operator"`
}

func (s *Server) halt(w http.ResponseWriter, r *http.Request) {
	s.writeHalt(w, r, false)
}

func (s *Server) freeze(w http.ResponseWriter, r *http.Request) {
	s.writeHalt(w, r, true)
}

func (s *Server) writeHalt(w http.ResponseWriter, r *http.Request, freeze bool) {
	if !s.workerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	var req haltReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rec := store.HaltRecord{Reason: req.Reason, Operator: req.Operator}
	var (
		rep engine.HaltReport
		err error
	)
	if freeze {
		rep, err = s.Eng.ApplyFreeze(req.Scope, req.Target, rec)
	} else {
		rep, err = s.Eng.Halt(req.Scope, req.Target, rec)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rep)
}

func (s *Server) unhalt(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	var req haltReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Eng.ResumeHalt(req.Scope, req.Target, store.HaltRecord{Reason: req.Reason, Operator: req.Operator}); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"halted": false, "scope": req.Scope, "target": req.Target})
}

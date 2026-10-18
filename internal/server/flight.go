package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/sannrox/rusui/internal/flight"
	"github.com/sannrox/rusui/internal/store"
)

func (s *Server) loadFlight(r *http.Request) (flight.Record, *store.Session, int) {
	if !s.operatorOrWorkerOK(r) {
		return flight.Record{}, nil, http.StatusUnauthorized
	}
	sid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return flight.Record{}, nil, http.StatusBadRequest
	}
	tid, err := strconv.ParseInt(r.PathValue("tid"), 10, 64)
	if err != nil {
		return flight.Record{}, nil, http.StatusBadRequest
	}
	sess, err := store.GetSession(s.Eng.Store, sid)
	if err != nil || sess == nil || !s.sessionAllowed(r, sess) {
		return flight.Record{}, nil, http.StatusNotFound
	}
	turn, err := store.GetTurn(s.Eng.Store, tid)
	if err != nil || turn.SessionID != sid {
		return flight.Record{}, nil, http.StatusNotFound
	}
	rec, err := store.GetFlightRecord(s.Eng.Store, tid)
	if errors.Is(err, sql.ErrNoRows) {
		return flight.Record{}, nil, http.StatusNotFound
	}
	if err != nil {
		return flight.Record{}, nil, http.StatusInternalServerError
	}
	return rec, sess, 0
}

func (s *Server) getFlight(w http.ResponseWriter, r *http.Request) {
	rec, _, code := s.loadFlight(r)
	if code != 0 {
		if code == http.StatusUnauthorized {
			http.Error(w, "auth", code)
			return
		}
		http.Error(w, "not found", code)
		return
	}
	principal := s.flightPrincipal(r)
	view := flight.Open(rec, principal)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(view)
}

type replayRequest struct {
	Snapshot *flight.Snapshot `json:"snapshot"`
}

func (s *Server) replayFlight(w http.ResponseWriter, r *http.Request) {
	rec, _, code := s.loadFlight(r)
	if code != 0 {
		if code == http.StatusUnauthorized {
			http.Error(w, "auth", code)
			return
		}
		http.Error(w, "not found", code)
		return
	}
	principal := s.flightPrincipal(r)
	view := flight.Open(rec, principal)
	var req replayRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "json", http.StatusBadRequest)
			return
		}
	}
	live := view.Snapshot
	if req.Snapshot != nil {
		live = *req.Snapshot
	}
	sink := &flight.DenyEffects{}
	rep := flight.Replay(view.Record, live, sink)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rep)
}

func (s *Server) flightPrincipal(r *http.Request) string {
	p := s.identify(r)
	if p.Class == CredWorker {
		return store.DefaultTenantID
	}
	return s.operatorActor(p)
}

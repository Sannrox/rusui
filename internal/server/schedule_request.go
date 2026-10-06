package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/store"
)

func (s *Server) guestSchedule(w http.ResponseWriter, r *http.Request) {
	tid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || tid == 0 {
		http.Error(w, "turn", http.StatusNotFound)
		return
	}
	if _, err := store.GetTurn(s.Eng.Store, tid); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !s.runnerOrTurnOK(r, tid) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	var req engine.ScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rec, err := s.Eng.GuestScheduleRequest(tid, req)
	if errors.Is(err, engine.ErrTurnNotLeased) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(rec)
}

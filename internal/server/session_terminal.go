package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/sannrox/rusui/internal/store"
)

// sessionTerminal is the guest-readable snapshot of the one environment
// shell (ADR 0065). It is not the transcript.
func (s *Server) sessionTerminal(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	sess, err := store.GetSession(s.Eng.Store, id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !s.OperatorBrowserOK(r) && !s.sessionEventOK(r, sess.ID) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	envRow, err := store.GetEnvironment(s.Eng.Store, sess.EnvironmentID)
	if err != nil {
		http.Error(w, "environment missing", http.StatusNotFound)
		return
	}
	if envRow.Handle == "" || envRow.State != store.EnvReady {
		http.Error(w, "environment not ready", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"output": s.termSnapshot(envRow.ID)})
}

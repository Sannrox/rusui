package server

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/sannrox/rusui/internal/sessionexport"
	"github.com/sannrox/rusui/internal/store"
)

const sessionExportBodyCap = 1 << 20

// exportSession is operator-only. Worker, turn, and unauthenticated
// callers are refused. Guests never receive the signing key.
func (s *Server) exportSession(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorAPIOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	if _, err := s.visibleSession(r, id); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	ttl := sessionexport.DefaultTTL
	if raw := r.URL.Query().Get("ttl"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			http.Error(w, "ttl", http.StatusBadRequest)
			return
		}
		ttl = d
	}
	body, err := s.Eng.ExportSession(id, ttl, s.exportSecrets())
	if errors.Is(err, sessionexport.ErrNoKey) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if errors.Is(err, store.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// importSession is operator-only. It mints a new session after MAC, expiry,
// and policy admit of the source. It does not call Restore.
func (s *Server) importSession(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorAPIOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	project := r.URL.Query().Get("project")
	if project == "" {
		http.Error(w, "project", http.StatusBadRequest)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, sessionExportBodyCap+1))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(raw) > sessionExportBodyCap {
		http.Error(w, "too large", http.StatusRequestEntityTooLarge)
		return
	}
	id, err := s.Eng.ImportSession(project, raw)
	switch {
	case errors.Is(err, sessionexport.ErrNoKey), errors.Is(err, sessionexport.ErrSource):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, sessionexport.ErrBadMAC), errors.Is(err, sessionexport.ErrNoExpiry),
		errors.Is(err, sessionexport.ErrExpired), errors.Is(err, sessionexport.ErrMalformed):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"session_id":` + strconv.FormatInt(id, 10) + "}\n"))
	}
}

package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

func (s *Server) putWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorAPIOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	if err := r.ParseMultipartForm(env.WorkspaceUploadCap + 1<<20); err != nil {
		http.Error(w, "file", http.StatusBadRequest)
		return
	}
	if err := s.writeWorkspaceUpload(r, r.URL.Query().Get("path")); err != nil {
		http.Error(w, err.Error(), statusForUpload(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) consoleUploadFile(w http.ResponseWriter, r *http.Request) {
	if !s.consoleRequire(w, r) {
		return
	}
	if err := r.ParseMultipartForm(env.WorkspaceUploadCap + 1<<20); err != nil || r.FormValue("csrf") != s.consoleCSRF() {
		http.Error(w, "csrf", http.StatusForbidden)
		return
	}
	if err := s.writeWorkspaceUpload(r, r.FormValue("path")); err != nil {
		http.Error(w, err.Error(), statusForUpload(err))
		return
	}
	http.Redirect(w, r, "/console/sessions/"+r.PathValue("id"), http.StatusSeeOther)
}

func (s *Server) writeWorkspaceUpload(r *http.Request, rel string) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return fmt.Errorf("id")
	}
	sess, err := store.GetSession(s.Eng.Store, id)
	if err != nil {
		return errNotFound
	}
	if sess.State == "cancelled" {
		return fmt.Errorf("session cancelled")
	}
	envRow, err := store.GetEnvironment(s.Eng.Store, sess.EnvironmentID)
	if err != nil {
		return fmt.Errorf("environment missing")
	}
	if envRow.Handle == "" || envRow.State != store.EnvReady {
		return fmt.Errorf("environment not ready")
	}
	src, hdr, err := r.FormFile("file")
	if err != nil {
		return fmt.Errorf("file")
	}
	defer func() { _ = src.Close() }()
	if rel == "" && hdr != nil {
		rel = hdr.Filename
	}
	var wr env.WorkspaceWriter
	if envRow.Driver == env.KindContainer {
		c, ok := s.Eng.Container.(env.Container)
		if !ok {
			return fmt.Errorf("container driver required")
		}
		wr = c
	} else {
		p, ok := s.Eng.Env.(env.Process)
		if !ok {
			return fmt.Errorf("process driver required")
		}
		wr = p
	}
	return wr.WriteFile(envRow.Handle, rel, src, env.WorkspaceUploadCap)
}

var errNotFound = fmt.Errorf("not found")

func statusForUpload(err error) int {
	if err == nil {
		return http.StatusNoContent
	}
	if errors.Is(err, errNotFound) {
		return http.StatusNotFound
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not ready"), strings.Contains(msg, "cancelled"):
		return http.StatusConflict
	case strings.Contains(msg, "path"), strings.Contains(msg, "oversized"), strings.Contains(msg, "file"), strings.Contains(msg, "id"):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

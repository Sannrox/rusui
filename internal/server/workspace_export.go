package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

const workspaceExportWake = "operator workspace export"

func (s *Server) exportWorkspace(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorAPIOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	sess, err := s.visibleOwnedSession(r, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if sess.Archived {
		http.Error(w, "session archived", http.StatusConflict)
		return
	}
	cancelled, err := store.SessionCancelled(s.Eng.Store, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if cancelled {
		http.Error(w, "session cancelled", http.StatusConflict)
		return
	}
	src, cleanup, err := s.workspaceExportSource(sess)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		msg, code := workspaceExportStatus(err)
		http.Error(w, msg, code)
		return
	}
	if _, err := env.WorkspaceHEAD(src); err != nil {
		http.Error(w, "unpublished", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	_ = env.WriteWorkspaceExport(src, w)
}

func (s *Server) workspaceExportSource(sess *store.Session) (string, func(), error) {
	envRow, err := store.GetEnvironment(s.Eng.Store, sess.EnvironmentID)
	if err != nil {
		return "", nil, fmt.Errorf("environment missing")
	}
	if envRow.Handle == "" {
		return "", nil, fmt.Errorf("environment missing")
	}
	switch envRow.Driver {
	case env.KindContainer:
		return s.captureContainerWorkspace(sess.ID, envRow)
	case "", env.KindProcess:
		if _, err := os.Stat(envRow.Handle); err != nil {
			return "", nil, fmt.Errorf("environment missing")
		}
		return envRow.Handle, nil, nil
	default:
		return "", nil, fmt.Errorf("workspace unavailable")
	}
}

func (s *Server) captureContainerWorkspace(sessionID int64, envRow *store.Environment) (string, func(), error) {
	c, ok := s.Eng.Container.(env.Container)
	if !ok {
		return "", nil, fmt.Errorf("container driver required")
	}
	dest, err := os.MkdirTemp("", fmt.Sprintf("rusui-export-%d-*", os.Getpid()))
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dest) }
	if err := c.CaptureTree(envRow.Handle, dest); err == nil {
		return dest, cleanup, nil
	} else if envRow.State != store.EnvSleeping {
		cleanup()
		return "", nil, err
	}
	woke, wakeErr := s.Eng.WakeSessionEnvironment(sessionID, workspaceExportWake)
	if wakeErr != nil {
		cleanup()
		if errors.Is(wakeErr, engine.ErrArchived) {
			return "", nil, fmt.Errorf("session archived")
		}
		return "", nil, wakeErr
	}
	if err := c.CaptureTree(woke.Handle, dest); err != nil {
		cleanup()
		return "", nil, err
	}
	return dest, cleanup, nil
}

func workspaceExportStatus(err error) (string, int) {
	msg := err.Error()
	switch msg {
	case "session archived", "environment missing":
		return msg, http.StatusConflict
	default:
		return msg, http.StatusInternalServerError
	}
}

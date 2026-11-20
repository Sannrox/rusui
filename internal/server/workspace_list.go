package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

type workspaceListView struct {
	Names []string `json:"names"`
}

func (s *Server) listWorkspaceFiles(w http.ResponseWriter, r *http.Request) {
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
	if sess.State == "cancelled" {
		http.Error(w, "session cancelled", http.StatusConflict)
		return
	}
	envRow, err := store.GetEnvironment(s.Eng.Store, sess.EnvironmentID)
	if err != nil {
		http.Error(w, "environment missing", http.StatusConflict)
		return
	}
	if envRow.Handle == "" {
		http.Error(w, "environment missing", http.StatusConflict)
		return
	}
	if envRow.State != store.EnvReady {
		http.Error(w, "environment not ready", http.StatusConflict)
		return
	}
	rel := r.URL.Query().Get("path")
	if rel != "" {
		data, err := s.readWorkspace(envRow, rel)
		if err == nil {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			_, _ = w.Write(data)
			return
		}
		if !errors.Is(err, env.ErrWorkspaceNotFile) {
			msg, code := listWorkspaceStatus(err)
			http.Error(w, msg, code)
			return
		}
	}
	names, err := s.listWorkspace(envRow, rel)
	if err != nil {
		msg, code := listWorkspaceStatus(err)
		http.Error(w, msg, code)
		return
	}
	if names == nil {
		names = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(workspaceListView{Names: names})
}

func (s *Server) listWorkspace(envRow *store.Environment, rel string) ([]string, error) {
	switch envRow.Driver {
	case env.KindContainer:
		c, ok := s.Eng.Container.(env.Container)
		if !ok {
			return nil, fmt.Errorf("container driver required")
		}
		in, ok := c.Inspector()
		if !ok {
			return nil, fmt.Errorf("workspace unavailable")
		}
		return in.WorkspaceList(envRow.Handle, rel)
	case "", env.KindProcess:
		p, ok := s.Eng.Env.(env.Process)
		if !ok {
			return nil, fmt.Errorf("process driver required")
		}
		return p.ListWorkspace(envRow.Handle, rel)
	default:
		return nil, fmt.Errorf("file inspection is not available for this environment")
	}
}

func (s *Server) readWorkspace(envRow *store.Environment, rel string) ([]byte, error) {
	var rd env.WorkspaceReader
	if envRow.Driver == env.KindContainer {
		c, ok := s.Eng.Container.(env.Container)
		if !ok {
			return nil, fmt.Errorf("container driver required")
		}
		rd = c
	} else {
		p, ok := s.Eng.Env.(env.Process)
		if !ok {
			return nil, fmt.Errorf("process driver required")
		}
		rd = p
	}
	return rd.ReadFile(envRow.Handle, rel, env.WorkspaceUploadCap)
}

func listWorkspaceStatus(err error) (string, int) {
	switch {
	case errors.Is(err, env.ErrWorkspaceReplaced):
		return env.FileRootReplaced, http.StatusConflict
	case errors.Is(err, env.ErrWorkspacePath):
		return "path", http.StatusBadRequest
	case errors.Is(err, env.ErrWorkspaceMissing):
		return "missing", http.StatusBadRequest
	case errors.Is(err, env.ErrWorkspaceNotDir):
		return "not a directory", http.StatusBadRequest
	case errors.Is(err, env.ErrWorkspaceNotFile):
		return "not a file", http.StatusBadRequest
	case errors.Is(err, env.ErrWorkspaceOversized):
		return "oversized", http.StatusBadRequest
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not ready"), strings.Contains(msg, "cancelled"), strings.Contains(msg, "environment missing"):
		return msg, http.StatusConflict
	case strings.Contains(msg, "path"), strings.Contains(msg, "id"):
		return msg, http.StatusBadRequest
	default:
		return msg, http.StatusInternalServerError
	}
}

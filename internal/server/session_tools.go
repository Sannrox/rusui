package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/sannrox/rusui/internal/store"
)

func (s *Server) listSessionTools(w http.ResponseWriter, r *http.Request) {
	if !s.operatorOrWorkerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	sess, err := s.visibleSession(r, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	grants, err := store.SessionToolGrantNames(s.Eng.Store, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"tools":          store.SpaceToolSet(sess.ExecutionMode, grants),
		"grants":         grants,
		"retention":      sess.Retention,
		"write_override": sess.WriteOverride,
	})
}

func (s *Server) listSessionToolGrants(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorAPIOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	if _, err := s.visibleOwnedSession(r, id); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	list, err := store.ListSessionToolGrants(s.Eng.Store, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

func (s *Server) grantSessionTool(w http.ResponseWriter, r *http.Request) {
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
	if store.NormalizeExecutionMode(sess.ExecutionMode) == store.ExecutionUser {
		http.Error(w, store.ErrUserModeTools.Error(), http.StatusConflict)
		return
	}
	var req struct {
		Tool string `json:"tool"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		http.Error(w, "body", http.StatusBadRequest)
		return
	}
	actor := s.operatorActor(s.identify(r))
	var g store.SessionToolGrant
	err = s.Eng.Store.Tx(func(tx *sql.Tx) error {
		var inner error
		g, inner = store.GrantSessionToolTx(tx, id, req.Tool, actor)
		if inner != nil {
			return inner
		}
		body, err := json.Marshal(map[string]any{"tool": g.Tool, "created_by": g.CreatedBy})
		if err != nil {
			return err
		}
		return store.InsertActionTx(tx, store.Action{
			ID: fmt.Sprintf("tool-grant-%d-%s-%d", id, g.Tool, g.ID), SessionID: &id,
			Type: "tool.grant", ReasonCode: "recorded", EvidenceClass: "plane_observed",
			LimitSentence: "A write-capable tool was granted on a space-mode session.",
			Body:          string(body),
		})
	})
	switch {
	case errors.Is(err, store.ErrInvalidTool):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case errors.Is(err, store.ErrUserModeTools):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case errors.Is(err, store.ErrToolGrantExists):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(g)
}

func (s *Server) revokeSessionTool(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorAPIOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	if _, err := s.visibleOwnedSession(r, id); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	tool := store.NormalizeTool(r.PathValue("tool"))
	err = s.Eng.Store.Tx(func(tx *sql.Tx) error {
		g, inner := store.RevokeSessionToolTx(tx, id, tool)
		if inner != nil {
			return inner
		}
		body, err := json.Marshal(map[string]any{"tool": tool})
		if err != nil {
			return err
		}
		return store.InsertActionTx(tx, store.Action{
			ID: fmt.Sprintf("tool-revoke-%d-%s-%d", id, tool, g.ID), SessionID: &id,
			Type: "tool.revoke", ReasonCode: "recorded", EvidenceClass: "plane_observed",
			LimitSentence: "A write-capable tool grant was revoked on a space-mode session.",
			Body:          string(body),
		})
	})
	if errors.Is(err, store.ErrUnknownToolGrant) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

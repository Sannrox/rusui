package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

const environmentReceiptPageSize = 100

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	if !s.operatorOrWorkerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := store.ListSessions(s.Eng.Store, r.URL.Query().Get("project"), limit)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if list == nil {
		list = []store.Session{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	if !s.operatorOrWorkerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", 400)
		return
	}
	if r.URL.Query().Get("view") == "review-status" {
		s.getSessionReviewStatus(w, r, id)
		return
	}
	includeReceipts := r.URL.Query().Get("include") == "receipts"
	var receiptAfterID int64
	query := r.URL.Query()
	if includeReceipts && query.Has("receipt_after_id") {
		receiptAfterID, err = strconv.ParseInt(query.Get("receipt_after_id"), 10, 64)
		if err != nil || receiptAfterID < 0 {
			http.Error(w, "receipt_after_id", http.StatusBadRequest)
			return
		}
	}
	sess, err := store.GetSession(s.Eng.Store, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	turns, err := store.ListTurnsForSession(s.Eng.Store, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if turns == nil {
		turns = []store.Turn{}
	}
	processes, err := store.ListSessionProcesses(s.Eng.Store, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	envState := ""
	if envRow, err := store.GetEnvironment(s.Eng.Store, sess.EnvironmentID); err == nil {
		envState = envRow.State
	}
	var reviewResult any
	revisionID, payload, hasReview, err := store.LatestReviewForSession(s.Eng.Store, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if hasReview {
		var artifact json.RawMessage
		if !json.Valid([]byte(payload)) {
			http.Error(w, "invalid stored review", http.StatusInternalServerError)
			return
		}
		artifact = json.RawMessage(payload)
		actions, err := store.ListActionsForSession(s.Eng.Store, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		dryRun := make([]map[string]string, 0)
		for _, action := range actions {
			if action.ReviewRevisionID == nil || *action.ReviewRevisionID != revisionID {
				continue
			}
			dryRun = append(dryRun, map[string]string{
				"type": action.Type, "reason_code": action.ReasonCode,
				"evidence_class": action.EvidenceClass, "limit_sentence": action.LimitSentence,
				"body": action.Body,
			})
		}
		reviewResult = map[string]any{
			"revision_id": revisionID, "artifact": artifact, "dry_run_actions": dryRun,
		}
	}
	response := map[string]any{
		"session": sess, "turns": turns, "processes": processes,
		"environment_state": envState,
	}
	if reviewResult != nil {
		response["review_result"] = reviewResult
	}
	if includeReceipts {
		receipts, hasMore, err := store.ListEnvironmentReceiptPage(s.Eng.Store, id, receiptAfterID, environmentReceiptPageSize)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		response["environment_receipts"] = receipts
		if hasMore {
			response["environment_receipts_next_after_id"] = receipts[len(receipts)-1].ID
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

type reviewStatusResponse struct {
	TurnID          int64  `json:"turn_id"`
	TurnState       string `json:"turn_state"`
	PendingRevision int    `json:"pending_revision"`
	ClaimedRevision int    `json:"claimed_revision"`
}

func (s *Server) getSessionReviewStatus(w http.ResponseWriter, r *http.Request, sessionID int64) {
	turnID, err := strconv.ParseInt(r.URL.Query().Get("turn_id"), 10, 64)
	if err != nil || turnID <= 0 {
		http.Error(w, "turn_id", http.StatusBadRequest)
		return
	}
	turn, err := store.GetTurn(s.Eng.Store, turnID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && turn.SessionID != sessionID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reviewStatusResponse{
		TurnID: turn.ID, TurnState: turn.State,
		PendingRevision: turn.PendingRevision, ClaimedRevision: turn.ClaimedRevision,
	})
}

func (s *Server) attachSession(w http.ResponseWriter, r *http.Request) {
	if !s.operatorOrWorkerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", 400)
		return
	}
	sess, err := store.GetSession(s.Eng.Store, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	writeSSE(w, "session", sess)
	fl.Flush()
	seen := map[string]string{}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			turns, err := store.ListTurnsForSession(s.Eng.Store, id)
			if err != nil {
				return
			}
			for _, t := range turns {
				key := fmt.Sprintf("turn:%d", t.ID)
				if seen[key] != t.State {
					seen[key] = t.State
					writeSSE(w, "turn", t)
					fl.Flush()
				}
			}
			acts, err := store.ListActionsForSession(s.Eng.Store, id)
			if err != nil {
				return
			}
			for _, a := range acts {
				key := "action:" + a.ID
				if _, ok := seen[key]; !ok {
					seen[key] = a.Type
					writeSSE(w, "action", a)
					fl.Flush()
				}
			}
		}
	}
}

func (s *Server) followUpTurn(w http.ResponseWriter, r *http.Request) {
	if !s.operatorOrWorkerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", 400)
		return
	}
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	turnID, pending, err := s.Eng.PromptFollowUp(id, req.Prompt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"turn_id": turnID, "pending_revision": pending})
}

func (s *Server) cancelSession(w http.ResponseWriter, r *http.Request) {
	if !s.operatorOrWorkerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", 400)
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
	if cancelErr := s.Eng.CancelSession(id); cancelErr != nil {
		if sess.Kind == store.SessionKindLocal {
			process, processErr := store.LatestSumikaProcess(s.Eng.Store, id)
			if processErr == nil && process.CancelRequestedAt != nil {
				if current, stateErr := store.GetSession(s.Eng.Store, id); stateErr == nil {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusAccepted)
					_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "confirmed": current.State == "cancelled", "state": current.State})
					return
				}
			}
		}
		http.Error(w, cancelErr.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if sess.Kind == store.SessionKindLocal {
		current, err := store.GetSession(s.Eng.Store, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "confirmed": current.State == "cancelled", "state": current.State})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (s *Server) restartLocalSession(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	result, startErr := s.Eng.RestartLocalProcess(id)
	if result.Session == nil {
		if startErr == nil {
			startErr = fmt.Errorf("local session was not restarted")
		}
		http.Error(w, startErr.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	response := map[string]any{"session_id": result.Session.ID, "kind": "local", "session": result.Session, "process": result.Process}
	if startErr != nil {
		response["start_error"] = startErr.Error()
	}
	_ = json.NewEncoder(w).Encode(response)
}

func writeSSE(w http.ResponseWriter, event string, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
}

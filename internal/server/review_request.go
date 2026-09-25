package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/sannrox/rusui/internal/engine"
)

func (s *Server) requestReview(w http.ResponseWriter, r *http.Request) {
	if !s.operatorOrWorkerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	var req struct {
		Repo string `json:"repo"`
		Item int    `json:"item"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result, err := s.Eng.RequestReview(req.Repo, req.Item)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, engine.ErrReviewRequestInvalid):
			status = http.StatusBadRequest
		case errors.Is(err, engine.ErrReviewRepoUnbound), errors.Is(err, engine.ErrReviewDisabled),
			errors.Is(err, engine.ErrReviewPaused), errors.Is(err, engine.ErrReviewBudget),
			errors.Is(err, engine.ErrReviewRefreshBusy), errors.Is(err, engine.ErrReviewRefreshFailed):
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"session_id": result.SessionID, "turn_id": result.TurnID,
		"pending_revision": result.PendingRevision,
	})
}

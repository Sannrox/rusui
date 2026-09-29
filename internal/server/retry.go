package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/sannrox/rusui/internal/slack"
)

// retryFailed requeues failed review work like the Slack `retry` command
// (`rusui retry`): OWNER/REPO requeues every failed job in the repository,
// OWNER/REPO#ITEM one item. Operator only; it grants nothing new.
func (s *Server) retryFailed(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorBrowserOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	var req struct {
		Target string `json:"target"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		http.Error(w, "body", http.StatusBadRequest)
		return
	}
	repo, item := slack.SplitItem(strings.TrimSpace(req.Target))
	if !strings.Contains(repo, "/") {
		http.Error(w, "target must be OWNER/REPO or OWNER/REPO#ITEM", http.StatusBadRequest)
		return
	}
	out := map[string]any{"repo": repo}
	if item == 0 {
		n, err := s.Eng.RetryAll(repo, "operator-api")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out["retried"] = n
	} else {
		if err := s.Eng.OperatorRetry(repo, item, "operator-api"); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		out["item"], out["retried"] = item, 1
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/sannrox/rusui/internal/ops"
	"github.com/sannrox/rusui/internal/receipt"
)

// sessionReceipt exports the latest completed review as JSON or Markdown.
// SQLite stays authoritative; the body is not a recovery input (#248).
func (s *Server) sessionReceipt(w http.ResponseWriter, r *http.Request) {
	if !s.operatorOrWorkerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	if _, err := s.visibleSession(r, id); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	exp, err := receipt.FromStore(s.Eng.Store, id, s.exportSecrets())
	if errors.Is(err, receipt.ErrNoReview) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if wantsMarkdown(r) {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte(exp.Markdown()))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(exp)
}

func wantsMarkdown(r *http.Request) bool {
	if r.URL.Query().Get("format") == "md" {
		return true
	}
	return strings.Contains(r.Header.Get("Accept"), "text/markdown")
}

func (s *Server) exportSecrets() []string {
	getenv := s.DiagnoseEnv
	if getenv == nil {
		getenv = os.Getenv
	}
	out := append([]string(nil), ops.SecretValues(getenv)...)
	for _, v := range []string{s.WorkerSec, s.OperatorTok, s.WebhookSec, s.SlackSec, s.ModelKey, s.GitHubToken, s.AgentGitHubToken} {
		if v != "" {
			out = append(out, v)
		}
	}
	for _, v := range s.TenantToks {
		if v != "" {
			out = append(out, v)
		}
	}
	for _, v := range s.MemberToks {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/slack"
)

type Server struct {
	Eng        *engine.Engine
	WebhookSec string
	WorkerSec  string
	SlackSec   string
	SlackUsers map[string]bool
	PolicyPath string
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /hooks/github", s.githubHook)
	mux.HandleFunc("POST /hooks/slack", s.slackHook)
	mux.HandleFunc("POST /jobs/claim", s.claim)
	mux.HandleFunc("POST /jobs/{id}/heartbeat", s.heartbeat)
	mux.HandleFunc("POST /jobs/{id}/complete", s.complete)
	mux.HandleFunc("POST /jobs/{id}/fail", s.fail)
	return mux
}

func (s *Server) githubHook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if s.WebhookSec != "" && !gh.Verify(s.WebhookSec, r.Header.Get("X-Hub-Signature-256"), body) {
		http.Error(w, "bad sig", 401)
		return
	}
	id := r.Header.Get("X-GitHub-Delivery")
	if id == "" {
		id = r.Header.Get("X-GitHub-Delivery-ID")
	}
	if id == "" {
		sum := sha256.Sum256(body)
		id = hex.EncodeToString(sum[:])
	}
	repo, item, kind, err := gh.ParseWebhook(body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err := s.Eng.IngestWebhook(id, repo, item, kind); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(200)
	w.Write([]byte("ok"))
}

func (s *Server) workerOK(r *http.Request) bool {
	return r.Header.Get("Authorization") == "Bearer "+s.WorkerSec || r.Header.Get("X-Worker-Token") == s.WorkerSec
}

func (s *Server) claim(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", 401)
		return
	}
	var req struct {
		Repo string `json:"repo"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	c, err := s.Eng.Claim(req.Repo)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if c == nil {
		w.WriteHeader(204)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"job_id":             c.Job.ID,
		"lease_generation":   c.Job.LeaseGeneration,
		"claimed_revision":   c.Job.ClaimedRevision,
		"repo":               c.Job.Repo,
		"item":               c.Job.Item,
		"item_kind":          c.Job.ItemKind,
		"snapshot":           c.Snapshot,
		"item_hash":          c.ItemHash,
		"execution_deadline": c.Job.ExecutionDeadlineAt,
		"input":              s.Eng.BuildInput(c),
	})
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", 401)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		LeaseGeneration int `json:"lease_generation"`
		ClaimedRevision int `json:"claimed_revision"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if err := s.Eng.Heartbeat(id, req.LeaseGeneration, req.ClaimedRevision); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	w.WriteHeader(200)
}

func (s *Server) complete(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", 401)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		LeaseGeneration int             `json:"lease_generation"`
		ClaimedRevision int             `json:"claimed_revision"`
		Artifact        engine.Artifact `json:"artifact"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	out, err := s.Eng.Complete(id, req.LeaseGeneration, req.ClaimedRevision, req.Artifact)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	json.NewEncoder(w).Encode(out)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", 401)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		LeaseGeneration int `json:"lease_generation"`
		ClaimedRevision int `json:"claimed_revision"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	out, err := s.Eng.Fail(id, req.LeaseGeneration, req.ClaimedRevision)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	json.NewEncoder(w).Encode(out)
}

func (s *Server) slackHook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if ch, ok := slack.Challenge(body); ok {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(ch))
		return
	}
	if s.SlackSec != "" {
		if err := slack.Verify(s.SlackSec, r.Header.Get("X-Slack-Request-Timestamp"), r.Header.Get("X-Slack-Signature"), body, s.Eng.Clock.Now()); err != nil {
			http.Error(w, err.Error(), 401)
			return
		}
	}
	cmd := slack.ParseForm(body)
	if s.SlackUsers != nil && !s.SlackUsers[cmd.UserID] {
		http.Error(w, "user", 403)
		return
	}
	text, code := s.runSlack(cmd)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(slack.Reply(text))
}

func (s *Server) runSlack(cmd slack.Command) (string, int) {
	if cmd.Command == "" {
		return "empty command. status | pause [repo] | resume [repo] | sweep [repo] | retry [repo[#item]] | reload", 200
	}
	switch cmd.Command {
	case "pause":
		if err := s.Eng.SetPause(cmd.Arg, true); err != nil {
			return err.Error(), 200
		}
		if cmd.Arg == "" {
			return "paused globally", 200
		}
		return "paused " + cmd.Arg, 200
	case "resume":
		if err := s.Eng.SetPause(cmd.Arg, false); err != nil {
			return err.Error(), 200
		}
		if cmd.Arg == "" {
			return "resumed globally", 200
		}
		return "resumed " + cmd.Arg, 200
	case "sweep":
		if err := s.Eng.Sweep(cmd.Arg); err != nil {
			return err.Error(), 200
		}
		if cmd.Arg == "" {
			return "sweep queued for all policy repos", 200
		}
		return "sweep queued for " + cmd.Arg, 200
	case "retry":
		if cmd.Arg == "" {
			return "retry requires repo or repo#item", 200
		}
		repo, item := slack.SplitItem(cmd.Arg)
		if item == 0 {
			n, err := s.Eng.RetryAll(repo, cmd.UserID)
			if err != nil {
				return err.Error(), 200
			}
			return fmt.Sprintf("retried %d failed job(s) in %s", n, repo), 200
		}
		if err := s.Eng.OperatorRetry(repo, item, cmd.UserID); err != nil {
			return err.Error(), 200
		}
		return fmt.Sprintf("retried %s#%d", repo, item), 200
	case "status":
		st, err := s.Eng.Status(cmd.Arg)
		if err != nil {
			return err.Error(), 200
		}
		return strings.TrimSpace(st), 200
	case "reload":
		if s.PolicyPath == "" {
			return "no policy path configured", 200
		}
		raw, err := os.ReadFile(s.PolicyPath)
		if err != nil {
			return err.Error(), 200
		}
		if err := s.Eng.ReloadPolicyBytes(raw); err != nil {
			return err.Error(), 200
		}
		return "policy reloaded", 200
	case "implement":
		return "implement is rejected until v3", 200
	default:
		return "unknown command", 200
	}
}

package server

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/slack"
	"github.com/sannrox/rusui/internal/store"
)

type Server struct {
	Eng                 *engine.Engine
	WebhookSec          string
	WorkerSec           string
	OperatorTok         string
	SlackSec            string
	SlackUsers          map[string]bool
	PolicyPath          string
	ModelKey            string
	ModelOrigin         *url.URL
	GitHubToken         string
	GitHubTokens        gh.TokenSource
	GitOrigin           *url.URL
	GuestHTTPSOnly      bool
	Addr                string
	DiagnosePlaneURL    string
	DiagnoseLookRuntime func() (env.Runtime, error)
	DiagnoseEnv         func(string) string
	DiagnoseHTTP        *http.Client
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("POST /drain", s.drain)
	mux.HandleFunc("POST /hooks/github", s.githubHook)
	mux.HandleFunc("POST /hooks/events", s.eventsHook)
	mux.HandleFunc("POST /hooks/slack", s.slackHook)
	mux.HandleFunc("POST /runners/hello", s.runnerHello)
	mux.HandleFunc("POST /sessions/{id}/events", s.sessionEvents)
	mux.HandleFunc("POST /turns/{id}/actions", s.turnActions)
	mux.HandleFunc("POST /jobs/claim", s.claim)
	mux.HandleFunc("GET /sessions", s.listSessions)
	mux.HandleFunc("GET /sessions/{id}", s.getSession)
	mux.HandleFunc("GET /sessions/{id}/attach", s.attachSession)
	mux.HandleFunc("GET /sessions/{id}/logs", s.sessionLogs)
	mux.HandleFunc("GET /approvals", s.listApprovals)
	mux.HandleFunc("GET /approvals/{id}", s.getApproval)
	mux.HandleFunc("GET /proofs/{id}", s.getProof)
	mux.HandleFunc("POST /proofs", s.createProof)
	mux.HandleFunc("GET /console/signin", s.consoleSignIn)
	mux.HandleFunc("POST /console/signin", s.consoleSignIn)
	mux.HandleFunc("POST /console/logout", s.consoleLogout)
	mux.HandleFunc("GET /console/sessions", s.consoleSessions)
	mux.HandleFunc("GET /console/sessions/{id}", s.consoleSession)
	mux.HandleFunc("GET /console/sessions/{id}/events", s.consoleEvents)
	mux.HandleFunc("GET /console/sessions/{id}/files", s.consoleFile)
	mux.HandleFunc("POST /approvals/{id}", s.decideApproval)
	mux.HandleFunc("POST /sessions/{id}/turns", s.followUpTurn)
	mux.HandleFunc("POST /sessions/{id}/cancel", s.cancelSession)
	mux.HandleFunc("POST /projects/{slug}/sessions", s.createSession)
	mux.HandleFunc("POST /projects/{slug}/schedules", s.createSchedule)
	mux.HandleFunc("POST /jobs/{id}/heartbeat", s.heartbeat)
	mux.HandleFunc("POST /jobs/{id}/complete", s.complete)
	mux.HandleFunc("POST /jobs/{id}/fail", s.fail)
	mux.Handle("/model-proxy/", http.HandlerFunc(s.modelProxy))
	mux.Handle("/git-proxy/", http.HandlerFunc(s.gitProxy))
	return mux
}

const turnTokenTTL = engine.GrantTTL

func (s *Server) runnerHello(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Name == "" {
		req.Name = store.LocalRunnerName
	}
	if err := store.TouchRunner(s.Eng.Store, req.Name, s.Eng.Clock.Now()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(200)
}

func (s *Server) githubHook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if s.WebhookSec == "" || !gh.Verify(s.WebhookSec, r.Header.Get("X-Hub-Signature-256"), body) {
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

func (s *Server) eventsHook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sig := r.Header.Get("X-Rusui-Signature-256")
	if sig == "" {
		sig = r.Header.Get("X-Hub-Signature-256")
	}
	if s.WebhookSec == "" || !gh.Verify(s.WebhookSec, sig, body) {
		http.Error(w, "bad sig", http.StatusUnauthorized)
		return
	}
	var ev struct {
		DeliveryID string `json:"delivery_id"`
		Source     string `json:"source"`
		Repo       string `json:"repo"`
		Item       int    `json:"item"`
		ItemKind   string `json:"item_kind"`
		OccurredAt string `json:"occurred_at"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var occurred time.Time
	if ev.OccurredAt != "" {
		occurred, err = time.Parse(time.RFC3339, ev.OccurredAt)
		if err != nil {
			occurred, err = time.Parse(time.RFC3339Nano, ev.OccurredAt)
			if err != nil {
				http.Error(w, "occurred_at", http.StatusBadRequest)
				return
			}
		}
	}
	if err := s.Eng.IngestEvent(ev.DeliveryID, ev.Source, ev.Repo, ev.Item, ev.ItemKind, occurred); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) workerOK(r *http.Request) bool {
	if s.WorkerSec == "" {
		return false
	}
	return r.Header.Get("Authorization") == "Bearer "+s.WorkerSec || r.Header.Get("X-Worker-Token") == s.WorkerSec
}

func bearer(r *http.Request) string {
	if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return tok
	}
	return r.Header.Get("X-Worker-Token")
}

func (s *Server) turnOK(r *http.Request, turnID int64) bool {
	tok := bearer(r)
	if tok == "" {
		return false
	}
	sum := sha256.Sum256([]byte(tok))
	ok, err := store.TurnCredentialValid(s.Eng.Store, turnID, hex.EncodeToString(sum[:]), s.Eng.Clock.Now())
	return err == nil && ok
}

func (s *Server) runnerOrTurnOK(r *http.Request, turnID int64) bool {
	return s.workerOK(r) || s.turnOK(r, turnID)
}

func (s *Server) sessionEventOK(r *http.Request, sessionID int64) bool {
	if s.workerOK(r) {
		return true
	}
	tok := bearer(r)
	if tok == "" {
		return false
	}
	sum := sha256.Sum256([]byte(tok))
	sid, _, ok, err := store.TurnTokenSession(s.Eng.Store, hex.EncodeToString(sum[:]), s.Eng.Clock.Now())
	return err == nil && ok && sid == sessionID
}

func (s *Server) sessionEvents(w http.ResponseWriter, r *http.Request) {
	sid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || sid == 0 {
		http.Error(w, "session", http.StatusNotFound)
		return
	}
	if _, err := store.GetSession(s.Eng.Store, sid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "session", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !s.sessionEventOK(r, sid) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	var ev struct {
		DeliveryID string `json:"delivery_id"`
		Kind       string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Eng.IngestGuestEvent(sid, ev.DeliveryID, ev.Kind); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) turnActions(w http.ResponseWriter, r *http.Request) {
	tid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || tid == 0 {
		http.Error(w, "turn", http.StatusNotFound)
		return
	}
	if _, err := store.GetTurn(s.Eng.Store, tid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "turn", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !s.runnerOrTurnOK(r, tid) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	var rec struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
		Body   any    `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := s.Eng.IngestTurnAction(tid, rec.Type, rec.Reason, rec.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
}

func issueTurnToken(st *store.Store, turnID int64, gen int, now time.Time) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	token := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	exp := now.Add(turnTokenTTL)
	if err := store.PutTurnCredential(st, turnID, gen, hash, exp.UTC().Format(time.RFC3339Nano)); err != nil {
		return "", time.Time{}, err
	}
	turn, err := store.GetTurn(st, turnID)
	if err != nil {
		return "", time.Time{}, err
	}
	sess, err := store.GetSession(st, turn.SessionID)
	if err != nil {
		return "", time.Time{}, err
	}
	if err := store.DeleteGrantsForTurn(st, turnID); err != nil {
		return "", time.Time{}, err
	}
	if err := store.PutGrant(st, store.Grant{
		TokenHash: hash,
		Kind:      store.GrantTurn,
		SessionID: turn.SessionID,
		TurnID:    turnID,
		Repo:      sess.Repo,
		CanPush:   true,
		ExpiresAt: exp,
	}); err != nil {
		return "", time.Time{}, err
	}
	return token, exp, nil
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
	tok, exp, err := issueTurnToken(s.Eng.Store, c.Job.ID, c.Job.LeaseGeneration, s.Eng.Clock.Now())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	out := map[string]any{
		"job_id":             c.Job.ID,
		"turn_id":            c.Job.ID,
		"lease_generation":   c.Job.LeaseGeneration,
		"claimed_revision":   c.Job.ClaimedRevision,
		"repo":               c.Job.Repo,
		"item":               c.Job.Item,
		"item_kind":          c.Job.ItemKind,
		"snapshot":           c.Snapshot,
		"item_hash":          c.ItemHash,
		"execution_deadline": c.Job.ExecutionDeadlineAt,
		"turn_token":         tok,
		"turn_token_expires": exp.UTC().Format(time.RFC3339Nano),
		"input":              s.Eng.BuildInput(c),
	}
	if turn, err := store.GetTurn(s.Eng.Store, c.Job.ID); err == nil {
		out["session_id"] = turn.SessionID
		if sess, err := store.GetSession(s.Eng.Store, turn.SessionID); err == nil {
			out["guest_session_id"] = sess.GuestSessionID
			if envRow, err := store.GetEnvironment(s.Eng.Store, sess.EnvironmentID); err == nil {
				out["driver"] = envRow.Driver
				out["handle"] = envRow.Handle
				if envRow.Handle != "" && envRow.Driver != "container" {
					out["workspace"] = envRow.Handle
				}
				out["model_base_url"] = modelBaseURL(r, envRow.Driver)
				out["git_proxy_url"] = gitProxyBaseURL(r, envRow.Driver)
			}
			if p, ok := s.Eng.Policy.Project(sess.Project); ok {
				out["permissions"] = p.Permissions
			} else if rr, ok := s.Eng.Policy.Repo(c.Job.Repo); ok {
				if p, ok := s.Eng.Policy.Project(rr.Project); ok {
					out["permissions"] = p.Permissions
				}
			}
		}
	}
	if _, ok := out["model_base_url"]; !ok {
		out["model_base_url"] = modelBaseURL(r, "")
	}
	if _, ok := out["git_proxy_url"]; !ok {
		out["git_proxy_url"] = gitProxyBaseURL(r, "")
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if !s.runnerOrTurnOK(r, id) {
		http.Error(w, "auth", 401)
		return
	}
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
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if !s.runnerOrTurnOK(r, id) {
		http.Error(w, "auth", 401)
		return
	}
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
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if !s.runnerOrTurnOK(r, id) {
		http.Error(w, "auth", 401)
		return
	}
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
	if err := slack.Verify(s.SlackSec, r.Header.Get("X-Slack-Request-Timestamp"), r.Header.Get("X-Slack-Signature"), body, s.Eng.Clock.Now()); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
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
		return "empty command. status | pause [project] | resume [project] | sweep [repo] | retry [repo[#item]] | reload", 200
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

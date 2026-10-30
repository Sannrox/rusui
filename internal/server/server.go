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
	"sync"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/oidc"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/slack"
	"github.com/sannrox/rusui/internal/store"
)

type Server struct {
	Eng         *engine.Engine
	WebhookSec  string
	WorkerSec   string
	OperatorTok string
	// TenantToks maps tenant_id to a distinct operator bearer. The default
	// tenant uses OperatorTok. Worker and turn secrets are never keys here.
	TenantToks map[string]string
	// MemberToks maps an extra member bearer to its tenant. ActorToks maps
	// a bearer to the actor id it presents inside that tenant (#605).
	MemberToks       map[string]string
	ActorToks        map[string]string
	OIDC             *oidc.Verifier
	SlackSec         string
	SlackUsers       map[string]bool
	PolicyPath       string
	Guest            string // acp.GuestGrok (default) or acp.GuestClaude
	GuestVersion     string // set only when the operator names a pinned guest version
	GuestModel       string // RUSUI_GUEST_MODEL; copied into the Claude guest
	OTelEndpoint     string // unset: no export; a dead collector must not fail a turn
	ModelProvider    string // ProviderXAI (default) or ProviderAnthropic
	ModelKey         string
	ModelOrigin      *url.URL // operator model upstream; keyless forwarding allowed
	GitHubToken      string
	GitHubTokens     gh.TokenSource
	AgentGitHubToken string // operator credential for implement sessions (ADR 0015)
	// RepoTokens is set when plane publication is on (ADR 0044): implement
	// guests get no GitHub credential and git pushes use a token scoped to
	// the one repository.
	RepoTokens interface {
		RepoToken(repo string) (string, error)
	}
	NoCoauthorTrailer bool // RUSUI_DISABLE_COAUTHOR_TRAILER
	NoSessionTrailer  bool // RUSUI_DISABLE_SESSION_TRAILER
	// SessionURL, when set, makes the session trailer a link to the
	// session (RUSUI_SESSION_URL, #497); unset, the trailer is the id.
	SessionURL          *SessionURL
	GitOrigin           *url.URL
	GuestHTTPSOnly      bool
	Addr                string
	DiagnosePlaneURL    string
	DiagnoseLookRuntime func() (env.Runtime, error)
	DiagnoseEnv         func(string) string
	DiagnoseHTTP        *http.Client
	PreviewBase         string // different origin from the plane, e.g. http://127.0.0.1:9xxx
	// GitCommit is this plane binary's git commit. A runner hello with a
	// different known commit is refused (ADR 0086 version tuple).
	GitCommit string

	termMu sync.Mutex
	terms  map[int64]*termSess
}

func (s *Server) Handler() http.Handler {
	if s.Eng != nil && s.Eng.DefaultGuest == "" {
		s.Eng.DefaultGuest = s.guest()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/oidc", s.oidcMetadata)
	mux.HandleFunc("GET /auth/oidc/session", s.oidcSession)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("POST /drain", s.drain)
	mux.HandleFunc("POST /resume", s.resume)
	mux.HandleFunc("POST /halt", s.halt)
	mux.HandleFunc("POST /unhalt", s.unhalt)
	mux.HandleFunc("POST /freeze", s.freeze)
	mux.HandleFunc("POST /guests/conformance", s.qualifyGuest)
	mux.HandleFunc("POST /hooks/github", s.githubHook)
	mux.HandleFunc("POST /hooks/events", s.eventsHook)
	mux.HandleFunc("POST /hooks/sessions/{id}", s.sessionWebhookHook)
	mux.HandleFunc("POST /hooks/slack", s.slackHook)
	mux.HandleFunc("POST /runners/hello", s.runnerHello)
	mux.HandleFunc("POST /sessions/{id}/events", s.sessionEvents)
	mux.HandleFunc("POST /turns/{id}/actions", s.turnActions)
	mux.HandleFunc("POST /turns/{id}/schedule", s.guestSchedule)
	mux.HandleFunc("POST /jobs/claim", s.claim)
	mux.HandleFunc("GET /sessions", s.listSessions)
	mux.HandleFunc("GET /sessions/{id}", s.getSession)
	mux.HandleFunc("POST /reviews", s.requestReview)
	mux.HandleFunc("GET /sessions/{id}/attach", s.attachSession)
	mux.HandleFunc("GET /sessions/{id}/logs", s.sessionLogs)
	mux.HandleFunc("GET /sessions/{id}/envlog", s.sessionEnvlog)
	mux.HandleFunc("GET /sessions/{id}/read", s.readSession)
	mux.HandleFunc("GET /sessions/{id}/read/follow", s.followSessionRead)
	mux.HandleFunc("GET /sessions/{id}/turns/{tid}/flight", s.getFlight)
	mux.HandleFunc("POST /sessions/{id}/turns/{tid}/replay", s.replayFlight)
	mux.HandleFunc("GET /sessions/{id}/terminal", s.sessionTerminal)
	mux.HandleFunc("GET /approvals", s.listApprovals)
	mux.HandleFunc("GET /approvals/{id}", s.getApproval)
	mux.HandleFunc("GET /proofs/{id}", s.getProof)
	mux.HandleFunc("POST /proofs", s.createProof)
	mux.HandleFunc("GET /console/signin", s.consoleSignIn)
	mux.HandleFunc("POST /console/signin", s.consoleSignIn)
	mux.HandleFunc("POST /console/logout", s.consoleLogout)
	mux.HandleFunc("GET /console/sessions", s.consoleSessions)
	mux.HandleFunc("GET /console/sessions/{id}", s.consoleSession)
	mux.HandleFunc("POST /console/sessions/{id}/prompt", s.consolePrompt)
	mux.HandleFunc("GET /console/sessions/{id}/events", s.consoleEvents)
	mux.HandleFunc("GET /console/sessions/{id}/files", s.consoleFile)
	mux.HandleFunc("POST /console/sessions/{id}/files", s.consoleUploadFile)
	mux.HandleFunc("POST /sessions/{id}/workspace", s.putWorkspaceFile)
	mux.HandleFunc("GET /console/approvals", s.consoleApprovals)
	mux.HandleFunc("POST /console/approvals/{id}", s.consoleDecide)
	mux.HandleFunc("GET /console/environments", s.consoleEnvironments)
	mux.HandleFunc("GET /console/runners", s.consoleRunners)
	mux.HandleFunc("GET /console/receipts", s.consoleReceipts)
	mux.HandleFunc("GET /console/budgets", s.consoleBudgets)
	mux.HandleFunc("GET /console/health", s.consoleHealth)
	mux.HandleFunc("GET /console/sessions/{id}/terminal", s.consoleTerminal)
	mux.HandleFunc("POST /console/sessions/{id}/terminal/lease", s.consoleTermLease)
	mux.HandleFunc("POST /console/sessions/{id}/terminal/revoke", s.consoleTermRevoke)
	mux.HandleFunc("POST /console/sessions/{id}/terminal/input", s.consoleTermInput)
	mux.HandleFunc("GET /console/sessions/{id}/terminal/output", s.consoleTermOutput)
	mux.HandleFunc("POST /console/sessions/{id}/preview", s.consoleMintPreview)
	mux.HandleFunc("POST /approvals/{id}", s.decideApproval)
	mux.HandleFunc("POST /sessions/{id}/turns", s.followUpTurn)
	mux.HandleFunc("POST /sessions/{id}/children", s.createChild)
	mux.HandleFunc("DELETE /sessions/{id}/queued", s.dropQueuedPrompts)
	mux.HandleFunc("POST /sessions/{id}/cancel", s.cancelSession)
	mux.HandleFunc("GET /sessions/{id}/webhook", s.sessionWebhook)
	mux.HandleFunc("POST /sessions/{id}/webhook", s.sessionWebhook)
	mux.HandleFunc("POST /sessions/{id}/archive", s.archiveSession)
	mux.HandleFunc("POST /sessions/{id}/unarchive", s.unarchiveSession)
	mux.HandleFunc("GET /sessions/{id}/grants", s.listSessionGrants)
	mux.HandleFunc("POST /sessions/{id}/grants", s.grantSessionAccess)
	mux.HandleFunc("DELETE /sessions/{id}/grants/{tenant}", s.revokeSessionAccess)
	mux.HandleFunc("PUT /sessions/{id}/execution-mode", s.changeSessionExecution)
	mux.HandleFunc("GET /sessions/{id}/tools", s.listSessionTools)
	mux.HandleFunc("GET /sessions/{id}/tool-grants", s.listSessionToolGrants)
	mux.HandleFunc("POST /sessions/{id}/tool-grants", s.grantSessionTool)
	mux.HandleFunc("DELETE /sessions/{id}/tool-grants/{tool}", s.revokeSessionTool)
	mux.HandleFunc("PUT /sessions/{id}/retention", s.putSessionRetention)
	mux.HandleFunc("POST /sessions/{id}/retention/write-override", s.postSessionWriteOverride)
	mux.HandleFunc("POST /sessions/{id}/retention/delete", s.postSessionRetentionDelete)
	mux.HandleFunc("POST /sessions/{id}/write-actions", s.applySessionWrite)
	mux.HandleFunc("POST /sessions/{id}/draft-actions", s.applySessionDraft)
	mux.HandleFunc("POST /sessions/{id}/restart", s.restartLocalSession)
	mux.HandleFunc("GET /projects/{slug}/measurements", s.projectMeasurements)
	mux.HandleFunc("POST /results/{id}/dispositions", s.recordDisposition)
	mux.HandleFunc("GET /dispositions/comment-gate", s.commentGate)
	mux.HandleFunc("POST /retry", s.retryFailed)
	mux.HandleFunc("GET /projects/{slug}/guests", s.projectGuests)
	mux.HandleFunc("POST /projects/{slug}/sessions", s.createSession)
	mux.HandleFunc("POST /projects/{slug}/schedules", s.createSchedule)
	mux.HandleFunc("DELETE /projects/{slug}/schedules/{id}", s.deleteSchedule)
	mux.HandleFunc("POST /jobs/{id}/heartbeat", s.heartbeat)
	mux.HandleFunc("POST /jobs/{id}/complete", s.complete)
	mux.HandleFunc("POST /jobs/{id}/fail", s.fail)
	mux.Handle("/model-proxy/", http.HandlerFunc(s.modelProxy))
	mux.Handle("/git-proxy/", http.HandlerFunc(s.gitProxy))
	return s.withPrincipal(mux)
}

const turnTokenTTL = engine.GrantTTL

func (s *Server) runnerHello(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	var req struct {
		Name   string `json:"name"`
		Commit string `json:"commit"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Name == "" {
		req.Name = store.LocalRunnerName
	}
	if err := runnerCommitMatch(s.GitCommit, req.Commit); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err := store.TouchRunner(s.Eng.Store, req.Name, s.Eng.Clock.Now()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(200)
}

// runnerCommitMatch enforces the 1.0 version tuple when both binaries
// know their git commit. Dev builds ("unknown" or empty) skip the check.
func runnerCommitMatch(plane, runner string) error {
	if plane == "" || plane == "unknown" || runner == "" || runner == "unknown" {
		return nil
	}
	if plane != runner {
		return fmt.Errorf("runner commit %s does not match plane %s", runner, plane)
	}
	return nil
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
	ok, err := s.turnCredentialOK(r, turnID)
	return err == nil && ok
}

func (s *Server) turnCredentialOK(r *http.Request, turnID int64) (bool, error) {
	tok := bearer(r)
	if tok == "" {
		return false, nil
	}
	sum := sha256.Sum256([]byte(tok))
	return store.TurnCredentialValid(s.Eng.Store, turnID, hex.EncodeToString(sum[:]), s.Eng.Clock.Now())
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

const turnActionBodyCap = 64 << 10

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
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, turnActionBodyCap))
	if err := dec.Decode(&rec); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			http.Error(w, "action body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body, err := sanitizeTurnActionBody(rec.Type, rec.Body, turnActionSecrets(s, r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := s.Eng.IngestTurnAction(tid, rec.Type, rec.Reason, body)
	if errors.Is(err, engine.ErrTurnNotLeased) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if errors.Is(err, engine.ErrTurnActionBudget) {
		http.Error(w, err.Error(), http.StatusTooManyRequests)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
}

func turnActionSecrets(s *Server, r *http.Request) []string {
	var secrets []string
	if tok := bearer(r); tok != "" {
		secrets = append(secrets, tok)
	}
	if s != nil && s.WorkerSec != "" {
		secrets = append(secrets, s.WorkerSec)
	}
	return secrets
}

type turnSecrets struct {
	Runner string
	Guest  string
	Exp    time.Time
}

func mintTurnSecret() (raw, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	raw = hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(sum[:]), nil
}

func issueTurnSecrets(st *store.Store, turnID int64, gen int, now time.Time) (turnSecrets, error) {
	var out turnSecrets
	runner, runnerHash, err := mintTurnSecret()
	if err != nil {
		return out, err
	}
	guest, guestHash, err := mintTurnSecret()
	if err != nil {
		return out, err
	}
	exp := now.Add(turnTokenTTL)
	if err := store.PutTurnCredential(st, turnID, gen, runnerHash, exp.UTC().Format(time.RFC3339Nano)); err != nil {
		return out, err
	}
	turn, err := store.GetTurn(st, turnID)
	if err != nil {
		return out, err
	}
	sess, err := store.GetSession(st, turn.SessionID)
	if err != nil {
		return out, err
	}
	if err := store.DeleteGrantsForTurn(st, turnID); err != nil {
		return out, err
	}
	if err := store.PutGrant(st, store.Grant{
		TokenHash: guestHash,
		Kind:      store.GrantTurn,
		SessionID: turn.SessionID,
		TurnID:    turnID,
		Repo:      sess.Repo,
		CanPush:   true,
		ExpiresAt: exp,
	}); err != nil {
		return out, err
	}
	out.Runner, out.Guest, out.Exp = runner, guest, exp
	return out, nil
}

func issueTurnToken(st *store.Store, turnID int64, gen int, now time.Time) (string, time.Time, error) {
	sec, err := issueTurnSecrets(st, turnID, gen, now)
	return sec.Runner, sec.Exp, err
}

func (s *Server) claim(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", 401)
		return
	}
	var req struct {
		Repo  string   `json:"repo"`
		Repos []string `json:"repos"`
		// ClaimToken names this claim attempt. A runner that lost the
		// response retries with the same token and gets the same lease.
		ClaimToken string `json:"claim_token"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if len(req.ClaimToken) > 128 {
		http.Error(w, "claim: claim_token too long", http.StatusBadRequest)
		return
	}
	tokenHash := ""
	if req.ClaimToken != "" {
		sum := sha256.Sum256([]byte(req.ClaimToken))
		tokenHash = hex.EncodeToString(sum[:])
	}
	repos := append([]string(nil), req.Repos...)
	if req.Repo != "" {
		if len(repos) > 0 {
			http.Error(w, "claim: repo and repos", http.StatusBadRequest)
			return
		}
		repos = []string{req.Repo}
	}
	if len(repos) == 0 {
		http.Error(w, "claim: repository required", http.StatusBadRequest)
		return
	}
	var last *engine.Claim
	for _, repo := range repos {
		c, err := s.Eng.ClaimToken(repo, tokenHash)
		if err != nil {
			if (errors.Is(err, engine.ErrBudget) || errors.Is(err, engine.ErrPaused)) && len(repos) > 1 {
				continue
			}
			if errors.Is(err, engine.ErrBudget) {
				w.Header().Set("X-Rusui-Claim-Blocked", "budget")
			} else if errors.Is(err, engine.ErrPaused) {
				w.Header().Set("X-Rusui-Claim-Blocked", "paused")
			}
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if c != nil {
			last = c
			break
		}
	}
	if last == nil {
		w.WriteHeader(204)
		return
	}
	c := last
	turn, err := store.GetTurn(s.Eng.Store, c.Job.ID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	guestName, guestEntry, err := s.Eng.SessionGuest(turn.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	s.noteClaimedProvider(c.Job.ID, guestName)
	sec, err := issueTurnSecrets(s.Eng.Store, c.Job.ID, c.Job.LeaseGeneration, s.Eng.Clock.Now())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	tok, grant, exp := sec.Runner, sec.Guest, sec.Exp
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
		"guest_grant":        grant,
		"turn_token_expires": exp.UTC().Format(time.RFC3339Nano),
		"input":              s.Eng.BuildInput(c),
		"guest":              guestName,
		"guest_spec":         guestEntry,
	}
	if acp.ValidGuestModel(s.GuestModel) {
		out["guest_model"] = s.GuestModel
	}
	if turn, err := store.GetTurn(s.Eng.Store, c.Job.ID); err == nil {
		out["session_id"] = turn.SessionID
		if atts, err := store.ListPromptAttachments(s.Eng.Store, turn.SessionID, c.Job.ClaimedRevision); err == nil && len(atts) > 0 {
			listed := make([]map[string]any, 0, len(atts))
			for _, a := range atts {
				listed = append(listed, map[string]any{
					"name":   a.Name,
					"mime":   a.MIME,
					"digest": a.Digest,
					"path":   a.Path,
				})
			}
			out["attachments"] = listed
		}
		if sess, err := store.GetSession(s.Eng.Store, turn.SessionID); err == nil {
			out["guest_session_id"] = sess.GuestSessionID
			if sess.Mode != "" {
				out["mode"] = sess.Mode
			}
			if sess.ExecutionMode != "" {
				out["execution_mode"] = sess.ExecutionMode
			}
			if sess.Act != "" {
				out["act"] = sess.Act
			}
			if names, err := store.SessionToolGrantNames(s.Eng.Store, sess.ID); err == nil {
				out["tool_grants"] = names
			}
			if sess.Retention != "" {
				out["retention"] = sess.Retention
			}
			out["write_override"] = sess.WriteOverride
			if envRow, err := store.GetEnvironment(s.Eng.Store, sess.EnvironmentID); err == nil {
				out["driver"] = envRow.Driver
				out["handle"] = envRow.Handle
				if envRow.Handle != "" && envRow.Driver != "container" {
					out["workspace"] = envRow.Handle
				}
				out["model_base_url"] = modelBaseURL(r, envRow.Driver)
				if !policy.IsProjectKey(c.Job.Repo) {
					out["git_proxy_url"] = gitProxyBaseURL(r, envRow.Driver)
				}
			}
			if task, ok := s.Eng.ImplementTask(sess); ok {
				ship := policy.ShipPullRequest
				if p, pok := s.Eng.PolicySnapshot().Project(sess.Project); pok && p.Ship != "" {
					ship = p.Ship
				}
				out["ship"] = ship
				if ship == policy.ShipPushBase {
					if base := strings.TrimPrefix(task.Ref, "refs/heads/"); base != "" {
						out["ship_base"] = base
					}
				}
				out["publication"] = "plane"
			}
			if t := s.commitTrailers(sess); len(t) > 0 {
				out["commit_trailers"] = t
			}
			pol := s.Eng.PolicySnapshot()
			p, ok := pol.Project(sess.Project)
			if !ok {
				if rr, rok := pol.Repo(c.Job.Repo); rok {
					p, ok = pol.Project(rr.Project)
				}
			}
			if ok {
				out["permissions"] = p.Permissions
				if secrets := s.Eng.AssignmentSecrets(p); len(secrets) > 0 {
					out["secrets"] = secrets
				}
			}
			// Gateway mode applies to every session, project or not.
			if servers := pol.SessionMCPServers(p); len(servers) > 0 {
				out["mcp_servers"] = servers
			}
			if pol.MCPGateway != "" {
				out["mcp_gateway"] = pol.MCPGateway
			}
		}
	}
	if _, ok := out["model_base_url"]; !ok {
		out["model_base_url"] = modelBaseURL(r, "")
	}
	if _, ok := out["git_proxy_url"]; !ok && !policy.IsProjectKey(c.Job.Repo) {
		out["git_proxy_url"] = gitProxyBaseURL(r, "")
	}
	_ = json.NewEncoder(w).Encode(out)
}

// agentGitHubToken is unused: guests never receive a GitHub credential
// (#607). Kept so tests can assert the assignment stays empty.
func (s *Server) agentGitHubToken(*store.Session) string { return "" }

// implementFenced matches the runner's fence: implement sessions propose
// writes and cannot open pull requests or push the default branch.
func (s *Server) implementFenced(sess *store.Session) bool {
	_, ok := s.Eng.ImplementTask(sess)
	return ok
}

func (s *Server) guest() string {
	if s.Guest == "" {
		return acp.GuestGrok
	}
	return s.Guest
}

// CoauthorTrailer marks agent commits (ADR 0015 D3). The .invalid domain
// never resolves to a GitHub account.
const CoauthorTrailer = "Co-authored-by: rusui <noreply@rusui.invalid>"

// commitTrailers are stamped on every commit an operator session's agent
// makes. They are attribution, not authorization.
func (s *Server) commitTrailers(sess *store.Session) []string {
	if sess == nil || (sess.Kind != store.SessionKindRun && sess.Kind != store.SessionKindScheduled) {
		return nil
	}
	var out []string
	if !s.NoCoauthorTrailer {
		out = append(out, CoauthorTrailer)
	}
	if !s.NoSessionTrailer {
		if s.SessionURL != nil {
			out = append(out, "Rusui-Session: "+s.SessionURL.For(sess.ID))
		} else {
			out = append(out, fmt.Sprintf("Rusui-Session: %d", sess.ID))
		}
	}
	return out
}

// heartbeat answers 409 and 401 only when the lease or its credential is
// gone; the runner ends the turn on those (#472). A store failure is 500,
// which the runner retries.
func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if !s.workerOK(r) {
		ok, err := s.turnCredentialOK(r, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "auth", http.StatusUnauthorized)
			return
		}
	}
	var req struct {
		LeaseGeneration int     `json:"lease_generation"`
		ClaimedRevision int     `json:"claimed_revision"`
		AckSteerIDs     []int64 `json:"ack_steer_ids,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	steer, err := s.Eng.HeartbeatSteerReceived(id, req.LeaseGeneration, req.ClaimedRevision, req.AckSteerIDs)
	if engine.IsLeaseRejected(err) {
		http.Error(w, err.Error(), 409)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"steer": steer})
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
		AckSteerIDs     []int64         `json:"ack_steer_ids,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	out, err := s.Eng.CompleteWithSteers(id, req.LeaseGeneration, req.ClaimedRevision, req.Artifact, req.AckSteerIDs)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	s.exportMeasurement(id)
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
	s.exportMeasurement(id)
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

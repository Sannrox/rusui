package engine

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/core"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/flight"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

const (
	RetryLimit        = 3
	RefreshRetryLimit = 5
	HeartbeatEvery    = time.Minute
	Liveness          = 3 * time.Minute
	ExecDeadline      = 12 * time.Minute
	// RunExecDeadline is the hard cap for a pinned implement turn: long
	// enough for one package change, its tests, and opening the pull
	// request. Heartbeats do not extend it. Review and scheduled turns
	// stay on ExecDeadline.
	RunExecDeadline = 45 * time.Minute
	GrantTTL        = 10 * time.Minute
	OwnerTTL        = 2 * time.Minute
	FetchTimeout    = 30 * time.Second
	StaleAge        = 60 * 24 * time.Hour
	RefreshTick     = time.Second
	ReconcileEvery  = 5 * time.Minute
	CatchUpEvery    = 15 * time.Minute
	ApplyRetryEvery = time.Minute
	EventMaxAge     = 24 * time.Hour
)

type Engine struct {
	DefaultGuest string // RUSUI_GUEST fallback for projects without guest choices
	Store        *store.Store
	policy       *policy.Effective
	GitHub       gh.Client
	// Publisher is set only when plane publication is on (ADR 0044).
	Publisher PullPublisher
	// Comments posts live review comments when policy comment is on.
	Comments gh.CommentPublisher
	// Core is the plane-held definition-draft client (ADR 0084). A nil
	// Core records core.draft and refuses apply.
	Core    core.Client
	Clock   clock.Clock
	Log     *log.Logger
	HookID  string
	HookIDs map[string]string
	Notify  func(string)

	FetchTimeout time.Duration
	OwnerTTL     time.Duration
	ExecDeadline time.Duration
	Env          env.Driver
	Container    env.Driver
	// ConformanceKey signs and verifies generic ACP guest receipts
	// (ADR 0082). Without it no generic ACP guest is admitted.
	ConformanceKey       []byte
	EnvTTL               time.Duration
	EnvIdleSleep         time.Duration
	CancelEnvWait        time.Duration
	Tree                 TreeSource
	SnapshotRoot         string
	snapshotMu           sync.Mutex
	refreshMu            sync.Mutex
	policyMu             sync.RWMutex
	sumikaMu             sync.Mutex
	envOperationMu       sync.Mutex
	envOperations        map[int64]struct{}
	modelMu              sync.Mutex
	modelSummaries       map[int64]*modelProxySummary
	modelSummaryOverflow bool
	completeMu           sync.Mutex
	completing           map[int64]*completeLock
	writeMu              sync.Mutex
	writing              map[int64]*completeLock
	flightMu             sync.Mutex
	flightReads          map[int64][]flight.Object

	// Secrets are plane-held values keyed by policy secret id (ADR 0066).
	// Production loads them from RUSUI_SECRET_<ID> via SecretsFromEnv.
	Secrets map[string]string
	// RequestedSecrets, when non-empty, must be a subset of the project's
	// allowlist or claim refuses before the lease. Empty means inject the
	// allowlist. Tests set this; production leaves it empty.
	RequestedSecrets []string
	// PreCloneHooks and PreSetupHooks are plane-stored scripts keyed by
	// project slug (ADR 0067). Production loads them from hooks/<slug>/.
	PreCloneHooks map[string]string
	PreSetupHooks map[string]string
}

func New(st *store.Store, pol *policy.Effective, g gh.Client, clk clock.Clock) *Engine {
	e := &Engine{Store: st, policy: pol, GitHub: g, Clock: clk, Log: log.New(os.Stderr, "rusui: ", 0), FetchTimeout: FetchTimeout, OwnerTTL: OwnerTTL}
	e.Notify = func(msg string) { e.Log.Println(msg) }
	return e
}

func (e *Engine) exception(msg string) {
	e.Log.Println(msg)
	if e.Notify != nil {
		e.Notify(msg)
	}
}

func (e *Engine) now() time.Time { return e.Clock.Now().UTC() }

func (e *Engine) execDeadline() time.Duration {
	if e.ExecDeadline > 0 {
		return e.ExecDeadline
	}
	return ExecDeadline
}

func (e *Engine) ReloadPolicy(p *policy.Effective) {
	e.policyMu.Lock()
	e.policy = p
	e.policyMu.Unlock()
	_ = e.Store.Tx(func(tx *store.Tx) error {
		return store.RecordPolicyRevisionTx(tx, p.Hash, string(p.Raw))
	})
}

// PolicySnapshot returns the active immutable policy while synchronizing with reloads.
func (e *Engine) PolicySnapshot() *policy.Effective {
	e.policyMu.RLock()
	defer e.policyMu.RUnlock()
	return e.policy
}

func (e *Engine) IngestWebhook(deliveryID, repo string, item int, kind string) error {
	return e.Store.Tx(func(tx *store.Tx) error {
		ins, err := store.InsertDeliveryTx(tx, deliveryID, repo, item, kind, e.now())
		if err != nil {
			return err
		}
		if !ins {
			return nil
		}
		return store.EnsureRefreshQueuedTx(tx, repo, item, kind, false)
	})
}

func (e *Engine) IngestEvent(deliveryID, source, repo string, item int, kind string, occurredAt time.Time) error {
	if deliveryID == "" || repo == "" || item == 0 || kind == "" {
		return fmt.Errorf("event: delivery_id, repo, item, and item_kind required")
	}
	if !occurredAt.IsZero() && e.now().Sub(occurredAt) > EventMaxAge {
		return nil
	}
	at := occurredAt
	if at.IsZero() {
		at = e.now()
	}
	return e.Store.Tx(func(tx *store.Tx) error {
		ins, err := store.InsertEventTx(tx, deliveryID, source, repo, item, kind, at)
		if err != nil {
			return err
		}
		if !ins {
			return nil
		}
		return store.EnsureRefreshQueuedTx(tx, repo, item, kind, false)
	})
}

// ErrTurnNotLeased refuses a turn action that arrives after the turn
// stopped running.
var ErrTurnNotLeased = errors.New("action: turn not leased")

// ErrTurnActionType refuses an action type the plane does not record
// from POST /turns/{id}/actions.
var ErrTurnActionType = errors.New("action: type not allowed")

// ErrTurnActionBudget refuses further rows once the per-turn cap is hit.
var ErrTurnActionBudget = errors.New("action: turn budget exceeded")

const (
	// TurnActionRowCap bounds how many actions one leased turn may store.
	TurnActionRowCap = 4096
	// TurnActionByteCap bounds stored action body bytes for one turn.
	TurnActionByteCap = 4 << 20
)

// AllowedTurnActionType is the ingest allow-list. Transcript, permission,
// and approval types are runner-owned; unknown types are refused so a
// guest cannot grow the store with arbitrary rows.
func AllowedTurnActionType(typ string) bool {
	if typ == "" || len(typ) > 64 {
		return false
	}
	switch typ {
	case "acp.fs.read_text_file", "acp.fs.write_text_file",
		"acp.terminal.create", "acp.terminal.output", "acp.terminal.wait_for_exit",
		"acp.terminal.kill", "acp.terminal.release",
		"acp.session.request_permission", "acp.session.update", "acp.approval",
		"acp.unknown", "acp.initialize", "acp.update",
		"resume", "permission.request":
		return true
	}
	return strings.HasPrefix(typ, "provider.")
}

func (e *Engine) IngestTurnAction(turnID int64, typ, reason string, body any) (string, error) {
	if !AllowedTurnActionType(typ) {
		return "", fmt.Errorf("%w: %s", ErrTurnActionType, typ)
	}
	turn, err := store.GetTurn(e.Store, turnID)
	if err != nil {
		return "", err
	}
	sess, err := store.GetSession(e.Store, turn.SessionID)
	if err != nil {
		return "", err
	}
	payload := []byte("{}")
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return "", err
		}
	}
	if reason == "" {
		reason = "recorded"
	}
	sid, tid := turn.SessionID, turn.ID
	id, err := newActionID()
	if err != nil {
		return "", err
	}
	// The state check and the insert share one write transaction, so an
	// action is either recorded while the turn is leased, before its
	// completion commits, or refused. A followed read reads the state
	// before the entries, so it never ends ahead of a recorded action
	// (#437).
	if err := e.Store.Tx(func(tx *store.Tx) error {
		cur, err := store.GetTurnTx(tx, turnID)
		if err != nil {
			return err
		}
		if cur.State != "leased" {
			return fmt.Errorf("%w: turn is %s", ErrTurnNotLeased, cur.State)
		}
		rows, used, err := store.TurnActionUsageTx(tx, turnID)
		if err != nil {
			return err
		}
		if rows >= TurnActionRowCap || used+int64(len(payload)) > TurnActionByteCap {
			return ErrTurnActionBudget
		}
		return store.InsertActionTx(tx, store.Action{
			ID:            id,
			SessionID:     &sid,
			TurnID:        &tid,
			Repo:          sess.Repo,
			Item:          sess.Item,
			Type:          typ,
			ReasonCode:    reason,
			EvidenceClass: "plane_observed",
			LimitSentence: "ACP client recorded the request; no request bypasses stdio dispatch.",
			Body:          string(payload),
		})
	}); err != nil {
		return "", err
	}
	_ = store.NoteFirstEvent(e.Store, turnID, e.now())
	if typ == "resume" && (reason == "succeeded" || reason == "refused") {
		_ = store.NoteResume(e.Store, turnID, reason)
	}
	switch reason {
	case "denied":
		if typ == "acp.approval" {
			_ = store.NotePermission(e.Store, turnID, "deny")
		}
	case "allowed":
		if typ == "acp.approval" {
			_ = store.NotePermission(e.Store, turnID, "allow")
		}
	}
	return id, nil
}

func newActionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (e *Engine) IngestGuestEvent(sessionID int64, deliveryID, kind string) error {
	if deliveryID == "" {
		return fmt.Errorf("event: delivery_id required")
	}
	_ = kind
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return err
	}
	if err := e.Store.Tx(func(tx *store.Tx) error {
		_, err := store.InsertEventTx(tx, deliveryID, "guest", sess.Repo, sess.Item, sess.ItemKind, e.now())
		return err
	}); err != nil {
		return err
	}
	turns, err := store.ListTurnsForSession(e.Store, sessionID)
	if err != nil || len(turns) == 0 {
		return err
	}
	turnID := turns[len(turns)-1].ID
	for _, turn := range turns {
		if turn.State == "leased" {
			turnID = turn.ID
			break
		}
	}
	return store.NoteFirstEvent(e.Store, turnID, e.now())
}

// withoutReviewedContent drops items that already have a review job for
// their listed content, so the catch-up cap goes to items that can produce
// a new job (ADR 0038 D5). A newer GitHub updated_at brings an item back;
// an item without a listed updated_at is kept.
func (e *Engine) withoutReviewedContent(open []snapshot.Item) ([]snapshot.Item, error) {
	reviewed := map[string]map[int]string{}
	out := make([]snapshot.Item, 0, len(open))
	for _, it := range open {
		known, ok := reviewed[it.Repo]
		if !ok {
			var err error
			if known, err = store.ReviewedUpdatedAt(e.Store, it.Repo); err != nil {
				return nil, err
			}
			reviewed[it.Repo] = known
		}
		if at, ok := known[it.Item]; ok && it.UpdatedAt != "" && at >= it.UpdatedAt {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

func githubIntakeItem(kind string, item int) bool {
	if item <= 0 {
		return false
	}
	return kind == "issue" || kind == "pull"
}

func (e *Engine) CatchUpItem(repo string, item int, kind string) error {
	if !githubIntakeItem(kind, item) {
		return nil
	}
	return e.Store.Tx(func(tx *store.Tx) error {
		return store.EnsureRefreshQueuedTx(tx, repo, item, kind, false)
	})
}

func (e *Engine) CatchUpOpenAndLocal(open []snapshot.Item) error {
	fresh, err := e.withoutReviewedContent(open)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, it := range CatchUpAdvisoryBatch(fresh, e.PolicySnapshot()) {
		seen[fmt.Sprintf("%s#%d", it.Repo, it.Item)] = true
		if err := e.CatchUpItem(it.Repo, it.Item, it.ItemKind); err != nil {
			return err
		}
	}
	tracked, err := store.ListLocalTrackedItems(e.Store)
	if err != nil {
		return err
	}
	for _, t := range tracked {
		repo := t[0].(string)
		item := t[1].(int)
		kind := t[2].(string)
		k := fmt.Sprintf("%s#%d", repo, item)
		if seen[k] {
			continue
		}
		if err := e.CatchUpItem(repo, item, kind); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) ExpireRefreshOwners() error {
	now := e.now()
	return e.Store.Tx(func(tx *store.Tx) error {
		list, err := store.ListRefreshOwnersTx(tx)
		if err != nil {
			return err
		}
		for _, r := range list {
			dead := true
			if r.OwnerExpiresAt != "" {
				t, _ := time.Parse(time.RFC3339Nano, r.OwnerExpiresAt)
				dead = !now.Before(t)
			}
			if !dead {
				continue
			}
			n := r.RetryCount + 1
			st := "queued"
			if n >= RefreshRetryLimit {
				st = "failed"
			}
			backoff := min(time.Duration(1<<min(n-1, 6))*time.Second, time.Minute)
			if err = store.ReleaseExpiredRefreshOwnerTx(tx, r.Repo, r.Item, n, st, now.Add(backoff)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (e *Engine) StartupExpireOwners() error {
	return e.Store.Tx(func(tx *store.Tx) error {
		return store.RequeueAllRefreshOwnersTx(tx)
	})
}

func (e *Engine) StepRefresh() (bool, error) {
	e.refreshMu.Lock()
	defer e.refreshMu.Unlock()
	return e.stepRefresh("", 0, "", false)
}

func (e *Engine) stepRefresh(filterRepo string, filterItem int, expectedKind string, enforceReviewBudget bool) (bool, error) {
	var repo, kind string
	var item, gen int
	var force bool
	now := e.now()
	err := e.Store.Tx(func(tx *store.Tx) error {
		next, err := store.NextQueuedRefreshTx(tx, now, filterRepo, filterItem)
		if err != nil {
			if errors.Is(err, store.ErrNoRows) {
				return errNoWork
			}
			return err
		}
		repo, item, kind, gen, force = next.Repo, next.Item, next.ItemKind, next.Generation, next.Force
		newGen := gen + 1
		if err := store.TakeRefreshOwnershipTx(tx, repo, item, newGen, now.Add(e.OwnerTTL)); err != nil {
			return err
		}
		gen = newGen
		return nil
	})
	if err == errNoWork {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	it, ferr := e.GitHub.GetItem(repo, item, kind)
	if ferr == nil && expectedKind != "" && it.ItemKind != expectedKind {
		ferr = fmt.Errorf("expected a pull request")
	}
	budgetDenied := false
	ineligible := false
	err = e.Store.Tx(func(tx *store.Tx) error {
		own, err := store.RefreshOwnershipTx(tx, repo, item)
		if err != nil {
			return err
		}
		needs, f := own.NeedsAnother, own.Force
		if own.Generation != gen || own.Owner == 0 {
			return nil
		}
		if ferr != nil {
			n, _ := store.RefreshRetryCountTx(tx, repo, item)
			n++
			st := "queued"
			if n >= RefreshRetryLimit {
				st = "failed"
				e.exception(fmt.Sprintf("refresh retries exhausted %s#%d", repo, item))
			}
			return store.FailRefreshAttemptTx(tx, repo, item, n, st)
		}
		if err := e.admitTx(tx, it, force || f == 1, gen, enforceReviewBudget); err != nil {
			if errors.Is(err, ErrReviewIneligible) {
				ineligible = true
				return store.IdleRefreshTx(tx, repo, item)
			}
			if !errors.Is(err, ErrReviewBudget) {
				return err
			}
			budgetDenied = true
			state := "idle"
			forcePending := force || f == 1
			if needs == 1 || forcePending {
				state = "queued"
			}
			return store.DeferRefreshTx(tx, repo, item, forcePending, state)
		}
		if needs == 1 {
			return store.RequeueRefreshTx(tx, repo, item)
		}
		return store.FinishRefreshTx(tx, repo, item)
	})
	if err != nil {
		return true, err
	}
	if ferr == nil {
		e.maybeAdmitRepair(it)
	}
	if budgetDenied {
		return true, ErrReviewBudget
	}
	if ineligible {
		return true, ErrReviewIneligible
	}
	return true, nil
}

var errNoWork = fmt.Errorf("no work")

func (e *Engine) admitTx(tx *store.Tx, it snapshot.Item, force bool, gen int, enforceReviewBudget bool) error {
	curGen, err := store.RefreshGenerationTx(tx, it.Repo, it.Item)
	if err != nil && !errors.Is(err, store.ErrNoRows) {
		return err
	}
	if err == nil && curGen != gen {
		return nil
	}
	activePolicy := e.PolicySnapshot()
	access := activePolicy.ReviewAccess(it.Repo, false)
	if !access.Allowed {
		return nil
	}
	consumeInvalidations := false
	if force {
		n, err := store.CountPendingInvalidationsTx(tx, it.Repo, it.Item)
		if err != nil {
			return err
		}
		if n == 0 {
			force = false
		} else {
			consumeInvalidations = true
		}
	}
	h := snapshot.ItemHash(it)
	j, err := store.GetJobTx(tx, it.Repo, it.Item, "review")
	if err != nil {
		return err
	}
	if j != nil {
		mismatch, err := e.reviewTenantMismatchTx(tx, it.Repo, it.Item)
		if err != nil {
			return err
		}
		if mismatch {
			return nil
		}
	}
	if !AdvisoryEligible(it) && (j == nil || j.PendingRevision == 0) {
		if enforceReviewBudget {
			return ErrReviewIneligible
		}
		return nil
	}
	if j != nil && j.PendingRevision > 0 {
		pend, err := store.LoadSnapshotTx(tx, it.Repo, it.Item, j.PendingRevision)
		if err != nil {
			return err
		}
		if !force && snapshot.ItemHash(pend) == h {
			return nil
		}
	}
	if j == nil || j.PendingRevision == 0 {
		if !force {
			var lastHash string
			var lastRev int
			lastHash, lastRev, err = store.LatestSnapshotHashTx(tx, it.Repo, it.Item)
			if err != nil {
				return err
			}
			_ = lastRev
			if lastHash != "" && lastHash == h && (j == nil || j.State == "completed") {
				return nil
			}
		}
	}
	if enforceReviewBudget {
		n, err := store.CountReviewsToday(tx, it.Repo, e.now().Format("2006-01-02"))
		if err != nil {
			return err
		}
		if decision := policy.DecideReview(activePolicy, it.Repo, false, n); !decision.Allowed {
			return ErrReviewBudget
		}
	}
	if consumeInvalidations {
		if err := store.ConsumeInvalidationsTx(tx, it.Repo, it.Item); err != nil {
			return err
		}
	}
	rev := 1
	if j != nil {
		rev = j.PendingRevision + 1
	}
	if err := store.SaveSnapshotTx(tx, it.Repo, it.Item, rev, it); err != nil {
		return err
	}
	if j == nil {
		j = &store.Job{Repo: it.Repo, Item: it.Item, ItemKind: it.ItemKind, Lane: "review", PendingRevision: rev, State: "queued", RetryCount: 0}
		if err := store.InsertJobTx(tx, j); err != nil {
			return err
		}
		return e.stampReviewTenantTx(tx, it.Repo, it.Item)
	}
	j.PendingRevision = rev
	j.RetryCount = 0
	if j.State != "leased" {
		j.State = "queued"
	}
	return store.UpdateJobTx(tx, j)
}

func (e *Engine) expireLeaseTx(tx *store.Tx, j *store.Job) error {
	now := e.now()
	if j.State != "leased" {
		return nil
	}
	expired := j.LeaseExpiresAt != nil && !now.Before(*j.LeaseExpiresAt)
	dead := j.ExecutionDeadlineAt != nil && !now.Before(*j.ExecutionDeadlineAt)
	if !expired && !dead {
		return nil
	}
	previousGeneration := j.LeaseGeneration
	if dead {
		return e.failClosedTx(tx, j, previousGeneration, j.ClaimedRevision, true)
	}
	steers, err := store.PromoteSteersTx(tx, j.ID, previousGeneration)
	if err != nil {
		return err
	}
	if j.ClaimedRevision == j.PendingRevision {
		j.RetryCount++
	}
	j.LeaseGeneration++
	if steers > 0 {
		j.RetryCount = 0
		j.State = "queued"
		if j.ClaimedRevision == j.PendingRevision {
			if err := applyNextFollowUpTx(tx, j); err != nil {
				return err
			}
		}
	} else if j.ClaimedRevision == j.PendingRevision && j.RetryCount >= RetryLimit {
		// An exhausted revision is terminal: a queued prompt held behind
		// it starts now (#492).
		if err := applyNextFollowUpTx(tx, j); err != nil {
			return err
		}
		if j.ClaimedRevision < j.PendingRevision {
			j.RetryCount = 0
			j.State = "queued"
		} else {
			j.State = "failed"
			e.exception(fmt.Sprintf("review retry_limit exhausted %s#%d until operator retry", j.Repo, j.Item))
		}
	} else {
		j.State = "queued"
	}
	if err := store.UpdateJobTx(tx, j); err != nil {
		return err
	}
	if j.State == "failed" {
		if err := e.finishMeasurementTx(tx, j.ID, j.State, Artifact{}, now); err != nil {
			return err
		}
	}
	turn, err := store.GetTurnTx(tx, j.ID)
	if err != nil {
		return err
	}
	return store.TouchSessionEnvironmentTx(tx, turn.SessionID, now, now.Add(e.envTTL()))
}

// ExpireOverdueLeases fails turns whose execution deadline has passed and
// requeues lost-heartbeat leases. Claim also runs this per repo; Recover
// and the scheduler run it so a deadline-exhausted turn does not wait for
// the next claim.
func (e *Engine) ExpireOverdueLeases() error {
	return e.Store.Tx(func(tx *store.Tx) error {
		return e.expireDueLeasesTx(tx, "", "")
	})
}

func (e *Engine) expireDeadLeasesTx(tx *store.Tx, repo, lane string) error {
	return e.expireDueLeasesTx(tx, repo, lane)
}

func (e *Engine) expireDueLeasesTx(tx *store.Tx, repo, lane string) error {
	jobs, err := store.ListDueLeasedJobsTx(tx, e.now(), repo, lane)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if err := e.expireLeaseTx(tx, j); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) Claim(repo string) (*Claim, error) {
	return e.ClaimToken(repo, "")
}

// ClaimToken claims the next turn for repo on behalf of a runner that names
// its claim attempt with tokenHash. If a live lease was already granted to
// that token, the same lease is returned again instead of a new turn: the
// runner retries with the token after losing the first response (#468).
func (e *Engine) ClaimToken(repo, tokenHash string) (*Claim, error) {
	var c *Claim
	replayed := false
	reviewCountDay := ""
	activePolicy := e.PolicySnapshot()
	err := e.Store.Tx(func(tx *store.Tx) error {
		if replay, err := e.replayClaimTx(tx, tokenHash); err != nil || replay != nil {
			c, replayed = replay, replay != nil
			return err
		}
		paused, err := e.repoPaused(tx, repo)
		if err != nil {
			return err
		}
		proj, repoPolicy, err := e.claimScope(tx, repo, paused)
		if err != nil {
			return err
		}
		// Each lane has its own gate: review needs review policy and budget;
		// run and scheduled need the project to admit that session kind.
		var lanes []string
		budgetOut := false
		if repoPolicy != nil && repoPolicy.Review {
			day := e.now().Format("2006-01-02")
			n, err := store.CountReviewsToday(tx, repo, day)
			if err != nil {
				return err
			}
			decision := policy.DecideReview(activePolicy, repo, false, n)
			if !decision.Allowed && decision.Reason == policy.ReviewReasonDailyBudget {
				// Reviews stop; run and scheduled work may still be claimed.
				budgetOut = true
				e.exception(fmt.Sprintf("daily review budget exhausted for %s", repo))
			} else if decision.Allowed {
				lanes = append(lanes, "review")
			}
		}
		for _, k := range []string{policy.KindRun, policy.KindScheduled} {
			if proj.AllowsKind(k) {
				lanes = append(lanes, k)
			}
		}
		if len(lanes) == 0 {
			if budgetOut {
				return errBudget
			}
			return errPolicy
		}
		keys := claimSelectRepos(repo, proj)
		for _, key := range keys {
			if err := e.expireDeadLeasesTx(tx, key, "review"); err != nil {
				return err
			}
			if err := e.expireDeadLeasesTx(tx, key, "run"); err != nil {
				return err
			}
			if err := e.expireDeadLeasesTx(tx, key, "scheduled"); err != nil {
				return err
			}
		}
		// A queued run is leased before older review or scheduled turns.
		// Turns in the same class stay in id order, so catch-up reviews
		// are still claimed once no run is waiting. A bound repository
		// also sees leftover pinless sessions of the same project (#557).
		queued, err := store.ListQueuedTurnsTx(tx, keys, lanes, sessionTenant(proj))
		if err != nil {
			return err
		}
		var id int64
		eligible := false
		for _, c := range queued {
			if e.environmentOperationInProgress(c.EnvironmentID) {
				continue
			}
			project := c.Project
			if project == "" {
				project = proj.Slug
			}
			halted, err := store.Halted(tx, c.Tenant, project, c.Act)
			if err != nil {
				return err
			}
			if halted {
				continue
			}
			name, _, _, err := store.SessionGuestTx(tx, c.SessionID)
			if err != nil {
				return err
			}
			if _, _, err := e.chooseGuest(proj.Slug, name, activePolicy); err != nil {
				if errors.Is(err, ErrGuestNotAllowed) {
					continue
				}
				return err
			}
			treeLeased, rootProject, err := store.CountTreeLeasedTurnsTx(tx, c.SessionID)
			if err != nil {
				return err
			}
			// Item reviews omit sessions.project; they are not a parent tree.
			if rootProject != "" {
				root, ok := activePolicy.Project(rootProject)
				if !ok || treeLeased >= root.MaxConcurrentLeases() {
					continue
				}
			}
			id, eligible = c.ID, true
			break
		}
		if !eligible {
			if budgetOut && len(queued) == 0 {
				return errBudget
			}
			return nil
		}
		j, err := store.GetJobByIDTx(tx, id)
		if err != nil {
			return err
		}
		leased, err := store.CountLeasedTurnsTx(tx, proj.Slug, proj.Repos)
		if err != nil {
			return err
		}
		if leased >= proj.MaxConcurrentLeases() {
			// The turn stays queued. Claim returns no work so the runner
			// retries without 409 (ADR 0064 D2/D3).
			return nil
		}
		turn, err := store.GetTurnTx(tx, id)
		if err != nil {
			return err
		}
		if _, _, err := e.bindSessionGuestTx(tx, turn.SessionID, proj.Slug, "", activePolicy); err != nil {
			return err
		}
		if err := e.checkRequestedSecrets(proj); err != nil {
			return err
		}
		now := e.now()
		exp := now.Add(Liveness)
		dead := now.Add(e.execDeadline())
		if j.Lane == policy.KindRun {
			dead = now.Add(RunExecDeadline)
		}
		if err := store.BeginMeasurementTx(tx, id, now); err != nil {
			return err
		}
		j.LeaseGeneration++
		j.ClaimedRevision = j.PendingRevision
		j.State = "leased"
		j.LeaseExpiresAt = &exp
		j.ExecutionDeadlineAt = &dead
		if err := store.UpdateJobTx(tx, j); err != nil {
			return err
		}
		if err := store.SetTurnClaimTokenTx(tx, j.ID, tokenHash); err != nil {
			return err
		}
		if err := store.SetTurnPolicyRevisionTx(tx, j.ID, activePolicy.Hash); err != nil {
			return err
		}
		if j.Lane == "review" {
			reviewCountDay = e.now().Format("2006-01-02")
			if err := store.IncrReviewsToday(tx, repo, reviewCountDay); err != nil {
				return err
			}
		}
		snap, err := store.LoadSnapshotTx(tx, j.Repo, j.Item, j.ClaimedRevision)
		if err != nil {
			return err
		}
		c = &Claim{Job: j, Snapshot: snap, ItemHash: snapshot.ItemHash(snap), reviewCountDay: reviewCountDay}
		return nil
	})
	if err != nil || c == nil || replayed {
		return c, err
	}
	if err := e.EnsureSessionEnvironment(c.Job.ID, c.Snapshot); err != nil {
		if errors.Is(err, store.ErrEnvironmentBusy) {
			if requeueErr := e.requeueBusyEnvironmentClaim(c); requeueErr != nil {
				return nil, fmt.Errorf("environment operation is busy: requeue claim: %w", requeueErr)
			}
			return nil, nil
		}
		_, failErr := e.Fail(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision)
		turn, _ := store.GetTurn(e.Store, c.Job.ID)
		state := "unknown"
		if turn != nil {
			if sess, getErr := store.GetSession(e.Store, turn.SessionID); getErr == nil {
				state = sess.EnvironmentState
			}
		}
		if failErr != nil {
			return nil, fmt.Errorf("environment setup failed with environment state %s: %w; failing turn: %w", state, err, failErr)
		}
		return nil, fmt.Errorf("environment setup failed with environment state %s: %w", state, err)
	}
	return c, nil
}

// replayClaimTx returns the live lease already granted to tokenHash, or
// nil when there is none. An expired lease or a passed execution deadline
// is not replayed; the ordinary claim path expires it.
func (e *Engine) replayClaimTx(tx *store.Tx, tokenHash string) (*Claim, error) {
	id, err := store.LeasedTurnByClaimTokenTx(tx, tokenHash)
	if err != nil || id == 0 {
		return nil, err
	}
	j, err := store.GetJobByIDTx(tx, id)
	if err != nil {
		return nil, err
	}
	now := e.now()
	if j.State != "leased" || (j.LeaseExpiresAt != nil && !now.Before(*j.LeaseExpiresAt)) ||
		(j.ExecutionDeadlineAt != nil && !now.Before(*j.ExecutionDeadlineAt)) {
		return nil, nil
	}
	snap, err := store.LoadSnapshotTx(tx, j.Repo, j.Item, j.ClaimedRevision)
	if err != nil {
		return nil, err
	}
	return &Claim{Job: j, Snapshot: snap, ItemHash: snapshot.ItemHash(snap)}, nil
}

func (e *Engine) requeueBusyEnvironmentClaim(c *Claim) error {
	return e.Store.Tx(func(tx *store.Tx) error {
		j, err := store.GetJobByIDTx(tx, c.Job.ID)
		if err != nil {
			return err
		}
		if j.State != "leased" || j.LeaseGeneration != c.Job.LeaseGeneration || j.ClaimedRevision != c.Job.ClaimedRevision {
			return errReject
		}
		j.State = "queued"
		j.LeaseExpiresAt = nil
		j.ExecutionDeadlineAt = nil
		if c.reviewCountDay != "" {
			if err := store.DecrReviewsToday(tx, j.Repo, c.reviewCountDay); err != nil {
				return fmt.Errorf("restore daily review budget: %w", err)
			}
		}
		return store.UpdateJobTx(tx, j)
	})
}

type Claim struct {
	Job            *store.Job
	Snapshot       snapshot.Item
	ItemHash       string
	reviewCountDay string
}

var (
	errPaused   = fmt.Errorf("paused")
	errHalted   = fmt.Errorf("halted")
	errPolicy   = fmt.Errorf("policy")
	errBudget   = fmt.Errorf("budget")
	errArchived = fmt.Errorf("archived")
	errSecret   = fmt.Errorf("secret")
	ErrBudget   = errBudget
	ErrPaused   = errPaused
	ErrHalted   = errHalted
	ErrArchived = errArchived
	ErrSecret   = errSecret
)

func (e *Engine) Heartbeat(jobID int64, gen, claimed int) error {
	_, err := e.HeartbeatSteer(jobID, gen, claimed)
	return err
}

func (e *Engine) HeartbeatSteer(jobID int64, gen, claimed int) (*Steer, error) {
	return e.HeartbeatSteerReceived(jobID, gen, claimed, nil)
}

func (e *Engine) HeartbeatSteerReceived(jobID int64, gen, claimed int, receivedSteerIDs []int64) (*Steer, error) {
	var next *Steer
	err := e.Store.Tx(func(tx *store.Tx) error {
		j, err := store.GetJobByIDTx(tx, jobID)
		if err != nil {
			return err
		}
		now := e.now()
		if j.State != "leased" || j.LeaseGeneration != gen || j.ClaimedRevision != claimed {
			return errReject
		}
		if j.LeaseExpiresAt != nil && !now.Before(*j.LeaseExpiresAt) {
			return errReject
		}
		if j.ExecutionDeadlineAt != nil && !now.Before(*j.ExecutionDeadlineAt) {
			return errReject
		}
		for _, steerID := range receivedSteerIDs {
			if err := store.ReceiveSteerTx(tx, j.ID, gen, steerID); err != nil {
				return errReject
			}
		}
		exp := now.Add(Liveness)
		j.LeaseExpiresAt = &exp
		if err := store.UpdateJobTx(tx, j); err != nil {
			return err
		}
		expAt := now.Add(GrantTTL).UTC().Format(time.RFC3339Nano)
		if err := store.RenewTurnCredentialTx(tx, jobID, gen, expAt); err != nil {
			return err
		}
		if err := store.RenewGrantTx(tx, jobID, expAt); err != nil {
			return err
		}
		turn, err := store.GetTurnTx(tx, jobID)
		if err != nil {
			return err
		}
		steer, err := store.NextSteerTx(tx, turn.ID, gen)
		if err != nil || steer == nil {
			return err
		}
		next = &Steer{ID: steer.ID, Prompt: steer.Prompt}
		return nil
	})
	return next, err
}

var errReject = fmt.Errorf("reject")

// IsLeaseRejected reports a lease that is not current: superseded,
// expired, past its deadline, or no longer leased.
func IsLeaseRejected(err error) bool { return errors.Is(err, errReject) }

type Artifact struct {
	SchemaVersion   int              `json:"schema_version"`
	Repo            string           `json:"repo"`
	Item            int              `json:"item"`
	ItemKind        string           `json:"item_kind"`
	ClaimedRevision int              `json:"claimed_revision"`
	SnapshotHash    string           `json:"snapshot_hash"`
	MainSHA         string           `json:"main_sha"`
	HeadSHA         string           `json:"head_sha"`
	Verdict         string           `json:"verdict"`
	Confidence      string           `json:"confidence"`
	ProposedActions []ProposedAction `json:"proposed_actions"`
	Publishable     map[string]any   `json:"publishable"`
	InputTokens     *int             `json:"input_tokens,omitempty"`
	OutputTokens    *int             `json:"output_tokens,omitempty"`
	GuestSessionID  string           `json:"guest_session_id,omitempty"`
	Result          *TaskResult      `json:"result,omitempty"`
}

type ProposedAction struct {
	Type       string     `json:"type"`
	ReasonCode string     `json:"reason_code"`
	Evidence   []Evidence `json:"evidence"`
	Canonical  int        `json:"canonical_item"`
	PRNumber   int        `json:"pr_number"`
	CommitSHA  string     `json:"commit_sha"`
}

type Evidence struct {
	Class string `json:"class"`
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

func (e *Engine) Complete(jobID int64, gen, claimed int, art Artifact) (map[string]any, error) {
	return e.CompleteWithSteers(jobID, gen, claimed, art, nil)
}

func (e *Engine) CompleteWithSteers(jobID int64, gen, claimed int, art Artifact, acknowledgedSteers []int64) (map[string]any, error) {
	// One Complete per job at a time: a concurrent duplicate waits, then
	// finds the lease completed, skips publishing, and returns the receipt.
	defer e.lockComplete(jobID)()
	// Outside the transaction: these may call GitHub. The transaction still
	// checks the lease and that art.Repo is the job's repository.
	e.publishResult(jobID, gen, claimed, &art)
	e.draftResult(jobID, gen, claimed, &art)
	e.observeResult(&art)
	e.observeFollowUpPublication(jobID, &art)
	var out map[string]any
	err := e.Store.Tx(func(tx *store.Tx) error {
		kind, payload, ok, err := store.GetReceiptTx(tx, jobID, gen, claimed)
		if err != nil {
			return err
		}
		if ok {
			if kind != "complete" {
				return errReject
			}
			json.Unmarshal([]byte(payload), &out)
			return nil
		}
		j, err := store.GetJobByIDTx(tx, jobID)
		if err != nil {
			return err
		}
		if err := e.acceptLease(tx, j, gen, claimed); err != nil {
			return err
		}
		snap, err := store.LoadSnapshotTx(tx, j.Repo, j.Item, j.ClaimedRevision)
		if err != nil {
			return err
		}
		wantHash := snapshot.ItemHash(snap)
		if art.Repo != j.Repo || art.Item != j.Item || art.ItemKind != j.ItemKind || art.ClaimedRevision != j.ClaimedRevision || art.SnapshotHash != wantHash {
			return e.finalizeFailureTx(tx, j, gen, claimed)
		}
		if j.ItemKind == "pull" && (art.HeadSHA != snap.HeadSHA) {
			return e.finalizeFailureTx(tx, j, gen, claimed)
		}
		if err := ValidateResult(art); err != nil {
			return e.finalizeFailureTx(tx, j, gen, claimed)
		}
		for _, steerID := range acknowledgedSteers {
			if err := store.AckSteerTx(tx, j.ID, gen, steerID); err != nil {
				return errReject
			}
		}
		turn, err := store.GetTurnTx(tx, j.ID)
		if err != nil {
			return err
		}
		now := e.now()
		if err := store.TouchSessionEnvironmentTx(tx, turn.SessionID, now, now.Add(e.envTTL())); err != nil {
			return err
		}
		art.MainSHA = snap.MainSHA
		art.SnapshotHash = wantHash
		b, _ := json.Marshal(art)
		revID, err := store.InsertReviewRevisionTx(tx, j.ID, j.ClaimedRevision, wantHash, snap.MainSHA, string(b))
		if err != nil {
			return err
		}
		receipt := map[string]any{"kind": "complete", "review_revision_id": revID}
		if art.Result != nil && art.Result.Outcome == OutcomeProposed && art.Result.Publish != nil {
			pid, err := newActionID()
			if err != nil {
				return err
			}
			body, err := json.Marshal(WriteProposal{
				Branch:       art.Result.Publish.Branch,
				Title:        art.Result.Publish.Title,
				Body:         art.Result.Publish.Body,
				CandidateSHA: art.Result.CandidateSHA,
			})
			if err != nil {
				return err
			}
			sid, tid := turn.SessionID, j.ID
			if err := store.InsertActionTx(tx, store.Action{
				ID: pid, SessionID: &sid, TurnID: &tid, ReviewRevisionID: &revID,
				Repo: j.Repo, Item: j.Item, Type: ActionWritePropose, ReasonCode: "recorded",
				EvidenceClass: "plane_observed",
				LimitSentence: "The turn proposed a GitHub write; applying it is a separate Action.",
				Body:          string(body),
			}); err != nil {
				return err
			}
			receipt["proposal_id"] = pid
		}
		if art.Result != nil && art.Result.Outcome == OutcomeProposed && art.Result.Draft != nil {
			pid, err := newActionID()
			if err != nil {
				return err
			}
			body, err := json.Marshal(art.Result.Draft)
			if err != nil {
				return err
			}
			sid, tid := turn.SessionID, j.ID
			if err := store.InsertActionTx(tx, store.Action{
				ID: pid, SessionID: &sid, TurnID: &tid, ReviewRevisionID: &revID,
				Repo: j.Repo, Item: j.Item, Type: ActionCoreDraft, ReasonCode: "recorded",
				EvidenceClass: "plane_observed",
				LimitSentence: "The turn proposed a definition draft; applying it is a separate Action.",
				Body:          string(body),
			}); err != nil {
				return err
			}
			receipt["draft_id"] = pid
		}
		rb, _ := json.Marshal(receipt)
		if err := store.InsertReceiptTx(tx, j.ID, gen, claimed, "complete", string(rb)); err != nil {
			return err
		}
		if err := e.recordFlightTx(tx, j.ID, receiptID(j.ID, gen, claimed)); err != nil {
			return err
		}
		if _, err := store.PromoteSteersTx(tx, j.ID, gen); err != nil {
			return err
		}
		out = receipt
		if turn, err := store.GetTurnTx(tx, j.ID); err == nil && art.GuestSessionID != "" {
			if err := store.SetGuestSessionTx(tx, turn.SessionID, art.GuestSessionID); err != nil {
				return err
			}
		}
		if j.ClaimedRevision == j.PendingRevision {
			if err := applyNextFollowUpTx(tx, j); err != nil {
				return err
			}
			if j.ClaimedRevision < j.PendingRevision {
				j.State = "queued"
				return store.UpdateJobTx(tx, j)
			}
			j.State = "completed"
			if err := store.UpdateJobTx(tx, j); err != nil {
				return err
			}
			if err := e.finishMeasurementTx(tx, j.ID, j.State, art, now); err != nil {
				return err
			}
			return e.maybeEnqueueApplyTx(tx, j, snap, revID, art)
		}
		j.State = "queued"
		return store.UpdateJobTx(tx, j)
	})
	if err != nil && art.Result != nil && art.Result.Publisher == PublisherPlane {
		// The pull request exists on GitHub even though the result was not
		// recorded; the operator needs to know.
		e.exception(fmt.Sprintf("publish job=%d: wrote %s#%d but Complete rejected the result: %v", jobID, art.Repo, art.Result.PullRequest, err))
	}
	if err == nil && art.Repo != "" {
		if pubErr := e.publishPendingComments(art.Repo, art.Item); pubErr != nil {
			e.exception(fmt.Sprintf("comment %s#%d: %v", art.Repo, art.Item, pubErr))
		}
	}
	return out, err
}

func (e *Engine) Fail(jobID int64, gen, claimed int) (map[string]any, error) {
	var out map[string]any
	err := e.Store.Tx(func(tx *store.Tx) error {
		_, payload, ok, err := store.GetReceiptTx(tx, jobID, gen, claimed)
		if err != nil {
			return err
		}
		if ok {
			json.Unmarshal([]byte(payload), &out)
			return nil
		}
		j, err := store.GetJobByIDTx(tx, jobID)
		if err != nil {
			return err
		}
		if err := e.acceptLeaseIdentity(j, gen, claimed); err != nil {
			return err
		}
		now := e.now()
		if j.ExecutionDeadlineAt != nil && !now.Before(*j.ExecutionDeadlineAt) {
			if err := e.failClosedTx(tx, j, gen, claimed, false); err != nil {
				return err
			}
		} else {
			if j.LeaseExpiresAt != nil && !now.Before(*j.LeaseExpiresAt) {
				return errReject
			}
			if err := e.finalizeFailureTx(tx, j, gen, claimed); err != nil {
				return err
			}
		}
		_, payload, _, err = store.GetReceiptTx(tx, jobID, gen, claimed)
		if err != nil {
			return err
		}
		json.Unmarshal([]byte(payload), &out)
		return nil
	})
	return out, err
}

// completeLock serializes Completes for one job; refs counts holders and
// waiters so the map entry is dropped when the last one leaves.
type completeLock struct {
	mu   sync.Mutex
	refs int
}

// lockComplete holds the job's Complete lock and returns its release. The
// lock is in-process: one plane process owns its SQLite database.
func (e *Engine) lockComplete(jobID int64) func() {
	e.completeMu.Lock()
	if e.completing == nil {
		e.completing = make(map[int64]*completeLock)
	}
	l := e.completing[jobID]
	if l == nil {
		l = &completeLock{}
		e.completing[jobID] = l
	}
	l.refs++
	e.completeMu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		e.completeMu.Lock()
		if l.refs--; l.refs == 0 {
			delete(e.completing, jobID)
		}
		e.completeMu.Unlock()
	}
}

func (e *Engine) acceptLeaseIdentity(j *store.Job, gen, claimed int) error {
	if j.State != "leased" || j.LeaseGeneration != gen || j.ClaimedRevision != claimed {
		return errReject
	}
	return nil
}

func (e *Engine) acceptLease(tx *store.Tx, j *store.Job, gen, claimed int) error {
	if err := e.acceptLeaseIdentity(j, gen, claimed); err != nil {
		return err
	}
	now := e.now()
	if j.LeaseExpiresAt != nil && !now.Before(*j.LeaseExpiresAt) {
		return errReject
	}
	if j.ExecutionDeadlineAt != nil && !now.Before(*j.ExecutionDeadlineAt) {
		return errReject
	}
	return nil
}

// failClosedTx records a fail receipt for the holding generation. The
// exhausted revision is not retried. A pending follow-up or promoted
// steer is queued as the next revision. bumpGeneration matches
// expireLeaseTx so a later Claim cannot steal the same generation.
func (e *Engine) failClosedTx(tx *store.Tx, j *store.Job, gen, claimed int, bumpGeneration bool) error {
	kind, _, ok, err := store.GetReceiptTx(tx, j.ID, gen, claimed)
	if err != nil {
		return err
	}
	if ok && kind != "fail" {
		return errReject
	}
	if !ok {
		receipt := map[string]any{"kind": "fail"}
		rb, _ := json.Marshal(receipt)
		if err := store.InsertReceiptTx(tx, j.ID, gen, claimed, "fail", string(rb)); err != nil {
			return err
		}
	}
	if _, err := store.PromoteSteersTx(tx, j.ID, gen); err != nil {
		return err
	}
	now := e.now()
	turn, err := store.GetTurnTx(tx, j.ID)
	if err != nil {
		return err
	}
	if err := store.TouchSessionEnvironmentTx(tx, turn.SessionID, now, now.Add(e.envTTL())); err != nil {
		return err
	}
	if bumpGeneration {
		j.LeaseGeneration++
	}
	if j.ClaimedRevision < j.PendingRevision {
		j.State = "queued"
		return store.UpdateJobTx(tx, j)
	}
	pending := j.PendingRevision
	if err := applyNextFollowUpTx(tx, j); err != nil {
		return err
	}
	if j.PendingRevision > pending {
		j.RetryCount = 0
		j.State = "queued"
		return store.UpdateJobTx(tx, j)
	}
	j.State = "failed"
	if err := store.UpdateJobTx(tx, j); err != nil {
		return err
	}
	e.exception(fmt.Sprintf("execution deadline exhausted %s#%d", j.Repo, j.Item))
	return e.finishMeasurementTx(tx, j.ID, j.State, Artifact{}, now)
}

func (e *Engine) finalizeFailureTx(tx *store.Tx, j *store.Job, gen, claimed int) error {
	turn, err := store.GetTurnTx(tx, j.ID)
	if err != nil {
		return err
	}
	now := e.now()
	if err := store.TouchSessionEnvironmentTx(tx, turn.SessionID, now, now.Add(e.envTTL())); err != nil {
		return err
	}
	receipt := map[string]any{"kind": "fail"}
	rb, _ := json.Marshal(receipt)
	if err := store.InsertReceiptTx(tx, j.ID, gen, claimed, "fail", string(rb)); err != nil {
		return err
	}
	steers, err := store.PromoteSteersTx(tx, j.ID, gen)
	if err != nil {
		return err
	}
	if j.ClaimedRevision < j.PendingRevision {
		j.State = "queued"
		return store.UpdateJobTx(tx, j)
	}
	j.RetryCount++
	if steers > 0 || j.RetryCount >= RetryLimit {
		// An exhausted revision is terminal: a queued prompt held behind
		// it starts now (#492).
		pending := j.PendingRevision
		if err := applyNextFollowUpTx(tx, j); err != nil {
			return err
		}
		if j.PendingRevision > pending {
			j.RetryCount = 0
			j.State = "queued"
		} else {
			j.State = "failed"
			e.exception(fmt.Sprintf("review retry_limit exhausted %s#%d until operator retry", j.Repo, j.Item))
		}
	} else {
		j.State = "queued"
	}
	if err := store.UpdateJobTx(tx, j); err != nil {
		return err
	}
	if j.State == "failed" {
		return e.finishMeasurementTx(tx, j.ID, j.State, Artifact{}, now)
	}
	return nil
}

func (e *Engine) maybeEnqueueApplyTx(tx *store.Tx, j *store.Job, snap snapshot.Item, revID int64, art Artifact) error {
	paused, err := e.repoPaused(tx, j.Repo)
	if err != nil || paused {
		return err
	}
	pol, ok := e.PolicySnapshot().Repo(j.Repo)
	if !ok {
		return nil
	}
	for _, a := range art.ProposedActions {
		if !eligible(art.Verdict, a, pol) {
			continue
		}
		okEv, class, sentence, err := e.verifyEvidence(j.Repo, snap, a)
		if err != nil || !okEv {
			continue
		}
		target := a.ReasonCode
		actionID := actionHash(j.Repo, j.Item, a.Type, revID, target)
		err = store.PlanApplyAttemptTx(tx, actionID, revID, j.Repo, j.Item)
		if err != nil {
			return err
		}
		_ = class
		_ = sentence
		return e.runApplyTx(tx, j, snap, revID, art, a, actionID, class, sentence)
	}
	return nil
}

func eligible(verdict string, a ProposedAction, pol policy.Repo) bool {
	if verdict == "propose_close" && a.Type == "close" {
		if !pol.Close {
			return false
		}
		switch a.ReasonCode {
		case "implemented_on_main", "duplicate_or_superseded", "stale_insufficient_info":
			return true
		default:
			return false
		}
	}
	if verdict == "propose_comment" && a.Type == "comment" && (pol.Comments || pol.Comment) {
		return true
	}
	return false
}

func (e *Engine) verifyEvidence(repo string, snap snapshot.Item, a ProposedAction) (bool, string, string, error) {
	switch a.ReasonCode {
	case "implemented_on_main":
		sent := "implemented_on_main: server verified the cited PR/commit is on default branch; not that it solves the reported problem"
		if snap.BaseRef != "" && snap.BaseRef != snap.DefaultBranch && !snap.MergedIntoDefault {
			return false, "server_verified", sent, nil
		}
		sha := a.CommitSHA
		if sha == "" {
			sha = snap.MergeCommitSHA
		}
		ok, err := e.GitHub.IsAncestor(repo, sha, snap.MainSHA)
		if err != nil || !ok {
			return false, "server_verified", sent, err
		}
		return true, "server_verified", sent, nil
	case "duplicate_or_superseded":
		if a.Canonical == 0 {
			return false, "model_assertion", "duplicate_or_superseded: server verified the canonical same-repo item exists; not that the reports are duplicates", nil
		}
		ok, err := e.GitHub.GetItemExists(repo, a.Canonical)
		if err != nil || !ok {
			return false, "server_verified", "", err
		}
		return true, "server_verified", "duplicate_or_superseded: server verified the canonical same-repo item exists; not that the reports are duplicates", nil
	case "stale_insufficient_info":
		created, _ := time.Parse(time.RFC3339, snap.CreatedAt)
		last := created
		if snap.LastNonBotCommentAt != "" {
			last, _ = time.Parse(time.RFC3339, snap.LastNonBotCommentAt)
		}
		now := e.now()
		if now.Sub(created) < StaleAge || now.Sub(last) < StaleAge {
			return false, "server_verified", "stale_insufficient_info: server verified the 60/60 day thresholds", nil
		}
		return true, "server_verified", "stale_insufficient_info: server verified the 60/60 day thresholds", nil
	case "not_reproducible_on_main", "incoherent":
		return false, "model_assertion", "advisory; not eligible to apply", nil
	default:
		if a.Type == "comment" {
			return true, "model_assertion", "model_assertion / advisory reasons: not eligible to apply", nil
		}
		return false, "model_assertion", "", nil
	}
}

func (e *Engine) runApplyTx(tx *store.Tx, j *store.Job, snap snapshot.Item, revID int64, art Artifact, a ProposedAction, actionID, class, sentence string) error {
	state, err := store.ApplyAttemptStateTx(tx, actionID)
	switch {
	case err == nil:
		if state == "succeeded" || state == "cancelled" || state == "failed" {
			return nil
		}
	case errors.Is(err, store.ErrNoRows):
	default:
		return err
	}
	paused, err := e.repoPaused(tx, j.Repo)
	if err != nil {
		return err
	}
	if paused {
		return store.CancelApplyAttemptTx(tx, actionID)
	}
	err = store.StartApplyAttemptTx(tx, actionID)
	if err != nil {
		return err
	}
	pol, ok := e.PolicySnapshot().Repo(j.Repo)
	if !ok || (a.Type == "close" && !pol.Close) || (a.Type == "comment" && !pol.Comments && !pol.Comment) {
		return store.CancelApplyAttemptTx(tx, actionID)
	}
	if j.ClaimedRevision != j.PendingRevision {
		return store.CancelApplyAttemptTx(tx, actionID)
	}
	pend, err := store.LoadSnapshotTx(tx, j.Repo, j.Item, j.PendingRevision)
	if err != nil {
		return err
	}
	if snapshot.ItemHash(pend) != snapshot.ItemHash(snap) {
		return store.CancelApplyAttemptTx(tx, actionID)
	}
	live := a.Type == "comment" && pol.Comment
	if live && (e.Comments == nil || liveCommentBlocked(snap)) {
		return store.CancelApplyAttemptTx(tx, actionID)
	}
	body := fmt.Sprintf("dry-run %s %s evidence_class=%s. %s", a.Type, a.ReasonCode, class, sentence)
	if live {
		pub, deny := publicCommentBody(a, actionID, class, sentence, art.Publishable)
		if deny != "" {
			return store.CancelApplyAttemptTx(tx, actionID)
		}
		body = pub
	}
	err = store.InsertApplyActionTx(tx, store.ApplyAction{
		ActionID: actionID, TurnID: j.ID, ReviewRevisionID: revID, Repo: j.Repo, Item: j.Item,
		Type: a.Type, ReasonCode: a.ReasonCode, EvidenceClass: class, LimitSentence: sentence, Body: body,
	})
	if err != nil {
		return err
	}
	if live {
		return nil
	}
	return store.SucceedApplyAttemptTx(tx, actionID)
}

func actionHash(repo string, item int, typ string, revID int64, target string) string {
	s := fmt.Sprintf("%s|%d|%s|%d|%s", repo, item, typ, revID, target)
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (e *Engine) ApplyAttempt(repo string, item int) error {
	var j *store.Job
	var revID int64
	var payload string
	err := e.Store.Tx(func(tx *store.Tx) error {
		var err error
		j, err = store.GetJobTx(tx, repo, item, "review")
		if err != nil || j == nil {
			return err
		}
		revID, payload, err = store.LatestReviewRevisionTx(tx, j.ID)
		return err
	})
	if err != nil {
		return err
	}
	var art Artifact
	if err := json.Unmarshal([]byte(payload), &art); err != nil {
		return err
	}
	live, err := e.GitHub.GetItem(repo, item, j.ItemKind)
	if err != nil {
		return err
	}
	err = e.Store.Tx(func(tx *store.Tx) error {
		j, err := store.GetJobTx(tx, repo, item, "review")
		if err != nil {
			return err
		}
		paused, err := e.repoPaused(tx, repo)
		if err != nil {
			return err
		}
		if paused {
			return nil
		}
		snap, err := store.LoadSnapshotTx(tx, repo, item, art.ClaimedRevision)
		if err != nil {
			return err
		}
		if snapshot.ItemHash(live) != snapshot.ItemHash(snap) {
			err = store.CancelRevisionApplyAttemptsTx(tx, revID)
			if err != nil {
				return err
			}
			return store.EnsureRefreshQueuedTx(tx, repo, item, j.ItemKind, false)
		}
		for _, a := range art.ProposedActions {
			if a.ReasonCode != "implemented_on_main" {
				continue
			}
			ok, _, _, err := e.verifyEvidence(repo, live, a)
			if err != nil {
				return err
			}
			if !ok {
				actionID := actionHash(repo, item, a.Type, revID, a.ReasonCode)
				err = store.CancelApplyAttemptTx(tx, actionID)
				if err != nil {
					return err
				}
				err = store.InvalidateEvidenceTx(tx, revID, actionID)
				if err != nil {
					return err
				}
				return store.EnsureRefreshQueuedTx(tx, repo, item, j.ItemKind, true)
			}
		}
		return e.maybeEnqueueApplyTx(tx, j, snap, revID, art)
	})
	if err != nil {
		return err
	}
	return e.publishPendingComments(repo, item)
}

func (e *Engine) repoPaused(tx *store.Tx, repo string) (bool, error) {
	slug := ""
	if s, ok := policy.ParseProjectKey(repo); ok {
		slug = s
	} else if r, ok := e.PolicySnapshot().Repo(repo); ok {
		slug = r.Project
	}
	return store.Paused(tx, slug)
}

// claimSelectRepos is the sessions.repo values a claim key may lease.
// A bound repository also sees leftover pinless sessions of the same
// project, which keep the project key so their empty pin stays valid (#557).
func claimSelectRepos(repo string, proj policy.Project) []string {
	if policy.IsProjectKey(repo) {
		return []string{repo}
	}
	pk := policy.ProjectKey(proj.Slug)
	if pk == repo {
		return []string{repo}
	}
	return []string{repo, pk}
}

// claimScope is the policy that admits a claim key. A project key is
// valid for a project with zero bound repositories (ADR 0048), and also
// while leftover pinless sessions still exist after the project later
// binds a repository (#557). New project-key claims stay refused when
// no such session remains.
func (e *Engine) claimScope(tx *store.Tx, repo string, paused bool) (policy.Project, *policy.Repo, error) {
	active := e.PolicySnapshot()
	if slug, ok := policy.ParseProjectKey(repo); ok {
		if paused {
			return policy.Project{}, nil, errPaused
		}
		p, ok := active.Project(slug)
		if !ok {
			return policy.Project{}, nil, errPolicy
		}
		if len(p.Repos) != 0 {
			open, err := store.HasOpenRepoSessionsTx(tx, repo)
			if err != nil {
				return policy.Project{}, nil, err
			}
			if !open {
				return policy.Project{}, nil, errPolicy
			}
		}
		return p, nil, nil
	}
	access := active.ReviewAccess(repo, paused)
	if access.Reason == policy.ReviewReasonPaused {
		return policy.Project{}, nil, errPaused
	}
	if access.Reason == policy.ReviewReasonInvalidRepo || access.Reason == policy.ReviewReasonUnboundRepo || access.Reason == policy.ReviewReasonMissingProject {
		return policy.Project{}, nil, errPolicy
	}
	rp, ok := active.Repo(repo)
	if !ok {
		return policy.Project{}, nil, errPolicy
	}
	p, ok := active.Project(rp.Project)
	if !ok {
		return policy.Project{}, nil, errPolicy
	}
	return p, &rp, nil
}

func (e *Engine) SetPause(project string, on bool) error {
	key := "pause:global"
	if project != "" {
		if _, ok := e.PolicySnapshot().Project(project); !ok {
			return fmt.Errorf("unknown project %q", project)
		}
		key = "pause:" + project
	}
	return e.Store.Tx(func(tx *store.Tx) error {
		v := "0"
		if on {
			v = "1"
		}
		return store.OverlaySet(tx, key, v)
	})
}

// OperatorRetry requeues a failed review job and appends a retry_audit row
// naming who asked: by.Actor is required, by.Delegate is recorded when an
// issuer-vouched subject retried through a delegating service.
func (e *Engine) OperatorRetry(repo string, item int, by store.Attribution) error {
	return e.Store.Tx(func(tx *store.Tx) error {
		j, err := store.GetJobTx(tx, repo, item, "review")
		if err != nil {
			return err
		}
		if j == nil {
			return fmt.Errorf("no review job")
		}
		if j.State != "failed" {
			return fmt.Errorf("not failed")
		}
		err = store.InsertRetryAuditTx(tx, j, by, e.now())
		if err != nil {
			return err
		}
		j.RetryCount = 0
		j.State = "queued"
		return store.UpdateJobTx(tx, j)
	})
}

func (e *Engine) Sweep(repo string) error {
	if repo == "" {
		for name := range e.PolicySnapshot().Repos {
			if err := e.Sweep(name); err != nil {
				return err
			}
		}
		return nil
	}
	list, err := store.ListRepoJobItems(e.Store, repo)
	if err != nil {
		return err
	}
	for _, x := range list {
		if err := e.CatchUpItem(repo, x.Item, x.ItemKind); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) RetryAll(repo, actor string) (int, error) {
	items, err := store.ListFailedReviewItems(e.Store, repo)
	if err != nil {
		return 0, err
	}
	for _, n := range items {
		if err := e.OperatorRetry(repo, n, store.Attribution{Actor: actor}); err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

func (e *Engine) Status(repo string) (string, error) {
	var b strings.Builder
	pausedG := false
	pausedR := false
	_ = e.Store.Tx(func(tx *store.Tx) error {
		var err error
		pausedG, err = store.Paused(tx, "")
		if err != nil {
			return err
		}
		if repo != "" {
			pausedR, err = e.repoPaused(tx, repo)
		}
		return err
	})
	fmt.Fprintf(&b, "pause global=%v", pausedG)
	if repo != "" {
		fmt.Fprintf(&b, " project=%v", pausedR)
	}
	b.WriteByte('\n')
	jobs, err := store.ListReviewJobs(e.Store, repo)
	if err != nil {
		return "", err
	}
	for _, j := range jobs {
		fmt.Fprintf(&b, "%s#%d %s pending=%d retry=%d\n", j.Repo, j.Item, j.State, j.PendingRevision, j.RetryCount)
	}
	if len(jobs) == 0 {
		b.WriteString("no jobs\n")
	}
	return b.String(), nil
}

func (e *Engine) ReloadPolicyBytes(raw []byte) error {
	p, err := policy.Parse(raw)
	if err != nil {
		return err
	}
	if err := p.VerifyConformance(e.ConformanceKey); err != nil {
		return err
	}
	e.ReloadPolicy(p)
	return nil
}

func (e *Engine) BuildInput(c *Claim) map[string]any {
	body := c.Snapshot.Body
	trunc := false
	if len(body) > 64*1024 {
		body = body[:64*1024]
		trunc = true
	}
	in := map[string]any{
		"schema_version":         1,
		"repo":                   c.Job.Repo,
		"item":                   c.Job.Item,
		"item_kind":              c.Job.ItemKind,
		"claimed_revision":       c.Job.ClaimedRevision,
		"item_hash":              c.ItemHash,
		"snapshot_hash":          c.ItemHash,
		"main_sha":               c.Snapshot.MainSHA,
		"head_sha":               c.Snapshot.HeadSHA,
		"base_sha":               c.Snapshot.BaseSHA,
		"title":                  c.Snapshot.Title,
		"body":                   body,
		"truncated":              trunc,
		"labels":                 c.Snapshot.Labels,
		"state":                  c.Snapshot.State,
		"linked_same_repo_items": c.Snapshot.LinkedSameRepoItems,
	}
	if prev, ok := priorPublication(e.Store, c.Job.ID); ok {
		in["published_pull_request"] = prev.PullRequest
		in["published_sha"] = prev.SHA
	}
	return in
}

func (e *Engine) ReconcileDeliveries(repo string) error {
	list, err := e.GitHub.ListDeliveries(repo)
	if err != nil {
		return err
	}
	var lastID string
	_ = e.Store.Tx(func(tx *store.Tx) error {
		_, lastID, _ = store.ReconcileCheckpointTx(tx, repo)
		return nil
	})
	for _, d := range slices.Backward(list) {
		if lastID != "" && d.ID == lastID {
			continue
		}
		fails, _ := store.DeliveryFailures(e.Store, d.ID)
		detail, err := e.GitHub.GetDelivery(repo, d.ID)
		if err != nil {
			fails++
			_ = e.Store.Tx(func(tx *store.Tx) error {
				return store.RecordDeliveryFailuresTx(tx, d.ID, repo, fails)
			})
			if fails >= RefreshRetryLimit {
				_ = e.Store.Tx(func(tx *store.Tx) error {
					return store.AdvanceReconcileCheckpointTx(tx, repo, d.DeliveredAt, d.ID)
				})
				continue
			}
			return err
		}
		r, item, kind, err := gh.ParseWebhook(detail.Payload)
		if err != nil {
			continue
		}
		if r == "" {
			r = repo
		}
		if err := e.IngestWebhook(detail.ID, r, item, kind); err != nil {
			return err
		}
		_ = e.Store.Tx(func(tx *store.Tx) error {
			return store.AdvanceReconcileCheckpointTx(tx, repo, detail.DeliveredAt, detail.ID)
		})
	}
	return nil
}

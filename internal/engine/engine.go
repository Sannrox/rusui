package engine

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/env"
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
	GrantTTL          = 10 * time.Minute
	OwnerTTL          = 2 * time.Minute
	FetchTimeout      = 30 * time.Second
	StaleAge          = 60 * 24 * time.Hour
	RefreshTick       = time.Second
	ReconcileEvery    = 5 * time.Minute
	CatchUpEvery      = 15 * time.Minute
	ApplyRetryEvery   = time.Minute
	EventMaxAge       = 24 * time.Hour
)

type Engine struct {
	Store   *store.Store
	policy  *policy.Effective
	GitHub  gh.Client
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
	EnvTTL       time.Duration
	Tree         TreeSource
	SnapshotRoot string
	policyMu     sync.RWMutex
	sumikaMu     sync.Mutex
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
	_ = e.Store.Tx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO policy_revisions(hash,payload) VALUES(?,?)`, p.Hash, string(p.Raw))
		return err
	})
}

// PolicySnapshot returns the active immutable policy while synchronizing with reloads.
func (e *Engine) PolicySnapshot() *policy.Effective {
	e.policyMu.RLock()
	defer e.policyMu.RUnlock()
	return e.policy
}

func (e *Engine) IngestWebhook(deliveryID, repo string, item int, kind string) error {
	return e.Store.Tx(func(tx *sql.Tx) error {
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
	return e.Store.Tx(func(tx *sql.Tx) error {
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

func (e *Engine) IngestTurnAction(turnID int64, typ, reason string, body any) (string, error) {
	if typ == "" {
		return "", fmt.Errorf("action: type required")
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
	return id, store.InsertAction(e.Store, store.Action{
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
	return e.Store.Tx(func(tx *sql.Tx) error {
		_, err := store.InsertEventTx(tx, deliveryID, "guest", sess.Repo, sess.Item, sess.ItemKind, e.now())
		return err
	})
}

func (e *Engine) CatchUpItem(repo string, item int, kind string) error {
	return e.Store.Tx(func(tx *sql.Tx) error {
		return store.EnsureRefreshQueuedTx(tx, repo, item, kind, false)
	})
}

func (e *Engine) CatchUpOpenAndLocal(open []snapshot.Item) error {
	seen := map[string]bool{}
	for _, it := range open {
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
	return e.Store.Tx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT repo, item, retry_count, owner_expires_at FROM refresh_requests WHERE owner=1`)
		if err != nil {
			return err
		}
		type rec struct {
			repo, exp string
			item, n   int
		}
		var list []rec
		for rows.Next() {
			var r rec
			var exp sql.NullString
			if err := rows.Scan(&r.repo, &r.item, &r.n, &exp); err != nil {
				rows.Close()
				return err
			}
			if exp.Valid {
				r.exp = exp.String
			}
			list = append(list, r)
		}
		rows.Close()
		for _, r := range list {
			dead := true
			if r.exp != "" {
				t, _ := time.Parse(time.RFC3339Nano, r.exp)
				dead = !now.Before(t)
			}
			if !dead {
				continue
			}
			n := r.n + 1
			st := "queued"
			if n >= RefreshRetryLimit {
				st = "failed"
			}
			backoff := min(time.Duration(1<<min(n-1, 6))*time.Second, time.Minute)
			nb := now.Add(backoff).Format(time.RFC3339Nano)
			if _, err = tx.Exec(`UPDATE refresh_requests SET owner=0, retry_count=?, state=?, not_before=?, generation=generation+1 WHERE repo=? AND item=?`,
				n, st, nb, r.repo, r.item); err != nil {
				return err
			}
		}
		return nil
	})
}

func (e *Engine) StartupExpireOwners() error {
	return e.Store.Tx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE refresh_requests SET owner=0, state='queued', generation=generation+1 WHERE owner=1`)
		return err
	})
}

func (e *Engine) StepRefresh() (bool, error) {
	var repo, kind string
	var item, gen int
	var force bool
	now := e.now()
	err := e.Store.Tx(func(tx *sql.Tx) error {
		row := tx.QueryRow(`SELECT repo, item, item_kind, generation, force FROM refresh_requests WHERE state='queued' AND owner=0 AND (not_before IS NULL OR not_before<=?) LIMIT 1`, now.Format(time.RFC3339Nano))
		var f int
		if err := row.Scan(&repo, &item, &kind, &gen, &f); err != nil {
			if err == sql.ErrNoRows {
				return errNoWork
			}
			return err
		}
		force = f == 1
		newGen := gen + 1
		exp := now.Add(e.OwnerTTL).Format(time.RFC3339Nano)
		_, err := tx.Exec(`UPDATE refresh_requests SET owner=1, generation=?, owner_expires_at=?, state='running' WHERE repo=? AND item=? AND owner=0`,
			newGen, exp, repo, item)
		if err != nil {
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
	err = e.Store.Tx(func(tx *sql.Tx) error {
		var curGen, owner, needs, f int
		if err := tx.QueryRow(`SELECT generation, owner, needs_another, force FROM refresh_requests WHERE repo=? AND item=?`, repo, item).Scan(&curGen, &owner, &needs, &f); err != nil {
			return err
		}
		if curGen != gen || owner == 0 {
			return nil
		}
		if ferr != nil {
			n := 0
			_ = tx.QueryRow(`SELECT retry_count FROM refresh_requests WHERE repo=? AND item=?`, repo, item).Scan(&n)
			n++
			st := "queued"
			if n >= RefreshRetryLimit {
				st = "failed"
				e.exception(fmt.Sprintf("refresh retries exhausted %s#%d", repo, item))
			}
			_, err := tx.Exec(`UPDATE refresh_requests SET owner=0, retry_count=?, state=? WHERE repo=? AND item=?`, n, st, repo, item)
			return err
		}
		if err := e.admitTx(tx, it, force || f == 1, gen); err != nil {
			return err
		}
		if needs == 1 {
			_, err := tx.Exec(`UPDATE refresh_requests SET owner=0, needs_another=0, force=0, state='queued' WHERE repo=? AND item=?`, repo, item)
			return err
		}
		_, err := tx.Exec(`UPDATE refresh_requests SET owner=0, force=0, state='idle' WHERE repo=? AND item=?`, repo, item)
		return err
	})
	if err != nil {
		return true, err
	}
	return true, nil
}

var errNoWork = fmt.Errorf("no work")

func (e *Engine) admitTx(tx *sql.Tx, it snapshot.Item, force bool, gen int) error {
	var curGen int
	err := tx.QueryRow(`SELECT generation FROM refresh_requests WHERE repo=? AND item=?`, it.Repo, it.Item).Scan(&curGen)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil && curGen != gen {
		return nil
	}
	if repoPolicy, ok := e.PolicySnapshot().Repo(it.Repo); !ok || !repoPolicy.Review {
		return nil
	}
	if force {
		var n int
		err := tx.QueryRow(`SELECT COUNT(*) FROM evidence_invalidations ei JOIN review_revisions r ON r.id=ei.review_revision_id JOIN jobs j ON j.id=r.job_id WHERE j.repo=? AND j.item=? AND ei.consumed=0`, it.Repo, it.Item).Scan(&n)
		if err != nil {
			return err
		}
		if n == 0 {
			force = false
		} else {
			_, err = tx.Exec(`UPDATE evidence_invalidations SET consumed=1 WHERE consumed=0 AND review_revision_id IN (SELECT r.id FROM review_revisions r JOIN jobs j ON j.id=r.job_id WHERE j.repo=? AND j.item=?)`, it.Repo, it.Item)
			if err != nil {
				return err
			}
		}
	}
	h := snapshot.ItemHash(it)
	j, err := store.GetJobTx(tx, it.Repo, it.Item, "review")
	if err != nil {
		return err
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
	rev := 1
	if j != nil {
		rev = j.PendingRevision + 1
	}
	if err := store.SaveSnapshotTx(tx, it.Repo, it.Item, rev, it); err != nil {
		return err
	}
	if j == nil {
		j = &store.Job{Repo: it.Repo, Item: it.Item, ItemKind: it.ItemKind, Lane: "review", PendingRevision: rev, State: "queued", RetryCount: 0}
		return store.InsertJobTx(tx, j)
	}
	j.PendingRevision = rev
	j.RetryCount = 0
	if j.State != "leased" {
		j.State = "queued"
	}
	return store.UpdateJobTx(tx, j)
}

func (e *Engine) expireLeaseTx(tx *sql.Tx, j *store.Job) error {
	now := e.now()
	if j.State != "leased" {
		return nil
	}
	expired := j.LeaseExpiresAt != nil && !now.Before(*j.LeaseExpiresAt)
	dead := j.ExecutionDeadlineAt != nil && !now.Before(*j.ExecutionDeadlineAt)
	if !expired && !dead {
		return nil
	}
	if j.ClaimedRevision == j.PendingRevision {
		j.RetryCount++
	}
	j.LeaseGeneration++
	if j.ClaimedRevision == j.PendingRevision && j.RetryCount >= RetryLimit {
		j.State = "failed"
		e.exception(fmt.Sprintf("review retry_limit exhausted %s#%d until operator retry", j.Repo, j.Item))
	} else {
		j.State = "queued"
	}
	return store.UpdateJobTx(tx, j)
}

func (e *Engine) expireDeadLeasesTx(tx *sql.Tx, repo, lane string) error {
	rows, err := tx.Query(`SELECT id FROM jobs WHERE repo=? AND lane=? AND state='leased'`, repo, lane)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		j, err := store.GetJobByIDTx(tx, id)
		if err != nil {
			return err
		}
		if err := e.expireLeaseTx(tx, j); err != nil {
			return err
		}
	}
	return nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func anySlice(xs []string) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

func (e *Engine) Claim(repo string) (*Claim, error) {
	var c *Claim
	activePolicy := e.PolicySnapshot()
	err := e.Store.Tx(func(tx *sql.Tx) error {
		paused, err := e.repoPaused(tx, repo)
		if err != nil {
			return err
		}
		if paused {
			return errPaused
		}
		repoPolicy, ok := activePolicy.Repo(repo)
		if !ok {
			return errPolicy
		}
		proj, ok := activePolicy.Project(repoPolicy.Project)
		if !ok {
			return errPolicy
		}
		// Each lane has its own gate: review needs review policy and budget;
		// run and scheduled need the project to admit that session kind.
		var lanes []string
		budgetOut := false
		if repoPolicy.Review {
			day := e.now().Format("2006-01-02")
			n, err := store.CountReviewsToday(tx, repo, day)
			if err != nil {
				return err
			}
			if n >= repoPolicy.MaxReviewsPerRepoPerUTCDay {
				// Reviews stop; run and scheduled work may still be claimed.
				budgetOut = true
				e.exception(fmt.Sprintf("daily review budget exhausted for %s", repo))
			} else {
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
		if err := e.expireDeadLeasesTx(tx, repo, "review"); err != nil {
			return err
		}
		if err := e.expireDeadLeasesTx(tx, repo, "run"); err != nil {
			return err
		}
		if err := e.expireDeadLeasesTx(tx, repo, "scheduled"); err != nil {
			return err
		}
		var id int64
		err = tx.QueryRow(`SELECT id FROM jobs WHERE repo=? AND state='queued' AND lane IN (`+placeholders(len(lanes))+`) ORDER BY id LIMIT 1`,
			append([]any{repo}, anySlice(lanes)...)...).Scan(&id)
		if err == sql.ErrNoRows {
			if budgetOut {
				return errBudget
			}
			return nil
		}
		if err != nil {
			return err
		}
		j, err := store.GetJobByIDTx(tx, id)
		if err != nil {
			return err
		}
		leased, err := store.CountLeasedTurnsTx(tx, repoPolicy.Project, proj.Repos)
		if err != nil {
			return err
		}
		if leased >= proj.MaxConcurrentLeases() {
			e.exception(fmt.Sprintf("concurrent lease cap exhausted for project %s", repoPolicy.Project))
			return errBudget
		}
		now := e.now()
		exp := now.Add(Liveness)
		dead := now.Add(e.execDeadline())
		j.LeaseGeneration++
		j.ClaimedRevision = j.PendingRevision
		j.State = "leased"
		j.LeaseExpiresAt = &exp
		j.ExecutionDeadlineAt = &dead
		if err := store.UpdateJobTx(tx, j); err != nil {
			return err
		}
		if j.Lane == "review" {
			if err := store.IncrReviewsToday(tx, repo, e.now().Format("2006-01-02")); err != nil {
				return err
			}
		}
		snap, err := store.LoadSnapshotTx(tx, j.Repo, j.Item, j.ClaimedRevision)
		if err != nil {
			return err
		}
		c = &Claim{Job: j, Snapshot: snap, ItemHash: snapshot.ItemHash(snap)}
		return nil
	})
	if err != nil || c == nil {
		return c, err
	}
	if err := e.EnsureSessionEnvironment(c.Job.ID, c.Snapshot); err != nil {
		return c, err
	}
	return c, nil
}

type Claim struct {
	Job      *store.Job
	Snapshot snapshot.Item
	ItemHash string
}

var (
	errPaused = fmt.Errorf("paused")
	errPolicy = fmt.Errorf("policy")
	errBudget = fmt.Errorf("budget")
	ErrBudget = errBudget
)

func (e *Engine) Heartbeat(jobID int64, gen, claimed int) error {
	return e.Store.Tx(func(tx *sql.Tx) error {
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
		exp := now.Add(Liveness)
		j.LeaseExpiresAt = &exp
		if err := store.UpdateJobTx(tx, j); err != nil {
			return err
		}
		expAt := now.Add(GrantTTL).UTC().Format(time.RFC3339Nano)
		if err := store.RenewTurnCredentialTx(tx, jobID, gen, expAt); err != nil {
			return err
		}
		return store.RenewGrantTx(tx, jobID, expAt)
	})
}

var errReject = fmt.Errorf("reject")

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
	InputTokens     int              `json:"input_tokens"`
	OutputTokens    int              `json:"output_tokens"`
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
	var out map[string]any
	err := e.Store.Tx(func(tx *sql.Tx) error {
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
		art.MainSHA = snap.MainSHA
		art.SnapshotHash = wantHash
		b, _ := json.Marshal(art)
		res, err := tx.Exec(`INSERT INTO review_revisions (job_id, claimed_revision, item_hash, main_sha, payload) VALUES (?,?,?,?,?)`,
			j.ID, j.ClaimedRevision, wantHash, snap.MainSHA, string(b))
		if err != nil {
			return err
		}
		revID, _ := res.LastInsertId()
		receipt := map[string]any{"kind": "complete", "review_revision_id": revID}
		rb, _ := json.Marshal(receipt)
		if err := store.InsertReceiptTx(tx, j.ID, gen, claimed, "complete", string(rb)); err != nil {
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
			return e.maybeEnqueueApplyTx(tx, j, snap, revID, art)
		}
		j.State = "queued"
		return store.UpdateJobTx(tx, j)
	})
	return out, err
}

func (e *Engine) Fail(jobID int64, gen, claimed int) (map[string]any, error) {
	var out map[string]any
	err := e.Store.Tx(func(tx *sql.Tx) error {
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
		if err := e.acceptLease(tx, j, gen, claimed); err != nil {
			return err
		}
		if err := e.finalizeFailureTx(tx, j, gen, claimed); err != nil {
			return err
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

func (e *Engine) acceptLease(tx *sql.Tx, j *store.Job, gen, claimed int) error {
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
	return nil
}

func (e *Engine) finalizeFailureTx(tx *sql.Tx, j *store.Job, gen, claimed int) error {
	receipt := map[string]any{"kind": "fail"}
	rb, _ := json.Marshal(receipt)
	if err := store.InsertReceiptTx(tx, j.ID, gen, claimed, "fail", string(rb)); err != nil {
		return err
	}
	if j.ClaimedRevision < j.PendingRevision {
		j.State = "queued"
		return store.UpdateJobTx(tx, j)
	}
	j.RetryCount++
	if j.RetryCount >= RetryLimit {
		j.State = "failed"
		e.exception(fmt.Sprintf("review retry_limit exhausted %s#%d until operator retry", j.Repo, j.Item))
	} else {
		j.State = "queued"
	}
	return store.UpdateJobTx(tx, j)
}

func (e *Engine) maybeEnqueueApplyTx(tx *sql.Tx, j *store.Job, snap snapshot.Item, revID int64, art Artifact) error {
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
		_, err = tx.Exec(`INSERT OR IGNORE INTO apply_attempts (action_id, review_revision_id, repo, item, state) VALUES (?,?,?,?, 'planned')`,
			actionID, revID, j.Repo, j.Item)
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
	if verdict == "propose_comment" && a.Type == "comment" && pol.Comments {
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

func (e *Engine) runApplyTx(tx *sql.Tx, j *store.Job, snap snapshot.Item, revID int64, art Artifact, a ProposedAction, actionID, class, sentence string) error {
	paused, err := e.repoPaused(tx, j.Repo)
	if err != nil {
		return err
	}
	if paused {
		_, err = tx.Exec(`UPDATE apply_attempts SET state='cancelled' WHERE action_id=?`, actionID)
		return err
	}
	_, err = tx.Exec(`UPDATE apply_attempts SET state='in_flight' WHERE action_id=?`, actionID)
	if err != nil {
		return err
	}
	pol, ok := e.PolicySnapshot().Repo(j.Repo)
	if !ok || (a.Type == "close" && !pol.Close) || (a.Type == "comment" && !pol.Comments) {
		_, err = tx.Exec(`UPDATE apply_attempts SET state='cancelled' WHERE action_id=?`, actionID)
		return err
	}
	if j.ClaimedRevision != j.PendingRevision {
		_, err = tx.Exec(`UPDATE apply_attempts SET state='cancelled' WHERE action_id=?`, actionID)
		return err
	}
	pend, err := store.LoadSnapshotTx(tx, j.Repo, j.Item, j.PendingRevision)
	if err != nil {
		return err
	}
	if snapshot.ItemHash(pend) != snapshot.ItemHash(snap) {
		_, err = tx.Exec(`UPDATE apply_attempts SET state='cancelled' WHERE action_id=?`, actionID)
		return err
	}
	body := fmt.Sprintf("dry-run %s %s evidence_class=%s. %s", a.Type, a.ReasonCode, class, sentence)
	var sid sql.NullInt64
	_ = tx.QueryRow(`SELECT id FROM sessions WHERE kind=? AND repo=? AND item=?`, store.SessionKindReview, j.Repo, j.Item).Scan(&sid)
	_, err = tx.Exec(`INSERT OR IGNORE INTO actions (action_id, session_id, turn_id, review_revision_id, repo, item, action_type, reason_code, evidence_class, limit_sentence, body) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		actionID, nullableInt(sid), j.ID, revID, j.Repo, j.Item, a.Type, a.ReasonCode, class, sentence, body)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE apply_attempts SET state='succeeded' WHERE action_id=?`, actionID)
	return err
}

func nullableInt(n sql.NullInt64) any {
	if !n.Valid {
		return nil
	}
	return n.Int64
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
	err := e.Store.Tx(func(tx *sql.Tx) error {
		var err error
		j, err = store.GetJobTx(tx, repo, item, "review")
		if err != nil || j == nil {
			return err
		}
		err = tx.QueryRow(`SELECT id, payload FROM review_revisions WHERE job_id=? ORDER BY id DESC LIMIT 1`, j.ID).Scan(&revID, &payload)
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
	return e.Store.Tx(func(tx *sql.Tx) error {
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
			_, err = tx.Exec(`UPDATE apply_attempts SET state='cancelled' WHERE review_revision_id=?`, revID)
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
				_, err = tx.Exec(`UPDATE apply_attempts SET state='cancelled' WHERE action_id=?`, actionID)
				if err != nil {
					return err
				}
				_, err = tx.Exec(`INSERT OR IGNORE INTO evidence_invalidations (review_revision_id, action_id, consumed) VALUES (?,?,0)`, revID, actionID)
				if err != nil {
					return err
				}
				return store.EnsureRefreshQueuedTx(tx, repo, item, j.ItemKind, true)
			}
		}
		return e.maybeEnqueueApplyTx(tx, j, snap, revID, art)
	})
}

func (e *Engine) repoPaused(tx *sql.Tx, repo string) (bool, error) {
	slug := ""
	if r, ok := e.PolicySnapshot().Repo(repo); ok {
		slug = r.Project
	}
	return store.Paused(tx, slug)
}

func (e *Engine) SetPause(project string, on bool) error {
	key := "pause:global"
	if project != "" {
		if _, ok := e.PolicySnapshot().Project(project); !ok {
			return fmt.Errorf("unknown project %q", project)
		}
		key = "pause:" + project
	}
	return e.Store.Tx(func(tx *sql.Tx) error {
		v := "0"
		if on {
			v = "1"
		}
		return store.OverlaySet(tx, key, v)
	})
}

func (e *Engine) OperatorRetry(repo string, item int, actor string) error {
	return e.Store.Tx(func(tx *sql.Tx) error {
		j, err := store.GetJobTx(tx, repo, item, "review")
		if err != nil || j == nil {
			return err
		}
		if j.State != "failed" {
			return fmt.Errorf("not failed")
		}
		_, err = tx.Exec(`INSERT INTO retry_audit (job_id, actor, ts, pending_revision, previous_retry_count) VALUES (?,?,?,?,?)`,
			j.ID, actor, e.now().Format(time.RFC3339Nano), j.PendingRevision, j.RetryCount)
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
	rows, err := e.Store.DB.Query(`SELECT item, item_kind FROM jobs WHERE repo=?`, repo)
	if err != nil {
		return err
	}
	defer rows.Close()
	type it struct {
		item int
		kind string
	}
	var list []it
	for rows.Next() {
		var x it
		if err := rows.Scan(&x.item, &x.kind); err != nil {
			return err
		}
		list = append(list, x)
	}
	for _, x := range list {
		if err := e.CatchUpItem(repo, x.item, x.kind); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) RetryAll(repo, actor string) (int, error) {
	rows, err := e.Store.DB.Query(`SELECT item FROM jobs WHERE repo=? AND lane='review' AND state='failed'`, repo)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var items []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return 0, err
		}
		items = append(items, n)
	}
	for _, n := range items {
		if err := e.OperatorRetry(repo, n, actor); err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

func (e *Engine) Status(repo string) (string, error) {
	var b strings.Builder
	pausedG := false
	pausedR := false
	_ = e.Store.Tx(func(tx *sql.Tx) error {
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
	q := `SELECT repo, item, state, pending_revision, retry_count FROM jobs WHERE lane='review'`
	args := []any{}
	if repo != "" {
		q += ` AND repo=?`
		args = append(args, repo)
	}
	q += ` ORDER BY repo, item`
	rows, err := e.Store.DB.Query(q, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var r, st string
		var item, pending, retry int
		if err := rows.Scan(&r, &item, &st, &pending, &retry); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s#%d %s pending=%d retry=%d\n", r, item, st, pending, retry)
		n++
	}
	if n == 0 {
		b.WriteString("no jobs\n")
	}
	return b.String(), nil
}

func (e *Engine) ReloadPolicyBytes(raw []byte) error {
	p, err := policy.Parse(raw)
	if err != nil {
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
	return map[string]any{
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
}

func (e *Engine) ReconcileDeliveries(repo string) error {
	list, err := e.GitHub.ListDeliveries(repo)
	if err != nil {
		return err
	}
	var lastAt, lastID string
	_ = e.Store.Tx(func(tx *sql.Tx) error {
		_ = tx.QueryRow(`SELECT last_delivered_at, last_delivery_id FROM reconcile_checkpoints WHERE repo=?`, repo).Scan(&lastAt, &lastID)
		return nil
	})
	for _, d := range slices.Backward(list) {
		if lastID != "" && d.ID == lastID {
			continue
		}
		var fails int
		_ = e.Store.DB.QueryRow(`SELECT retries FROM failed_deliveries WHERE delivery_id=?`, d.ID).Scan(&fails)
		detail, err := e.GitHub.GetDelivery(repo, d.ID)
		if err != nil {
			fails++
			_ = e.Store.Tx(func(tx *sql.Tx) error {
				_, err := tx.Exec(`INSERT INTO failed_deliveries(delivery_id,repo,retries) VALUES(?,?,?) ON CONFLICT(delivery_id) DO UPDATE SET retries=?`, d.ID, repo, fails, fails)
				return err
			})
			if fails >= RefreshRetryLimit {
				_ = e.Store.Tx(func(tx *sql.Tx) error {
					_, err := tx.Exec(`INSERT INTO reconcile_checkpoints(repo,last_delivered_at,last_delivery_id) VALUES(?,?,?) ON CONFLICT(repo) DO UPDATE SET last_delivered_at=excluded.last_delivered_at, last_delivery_id=excluded.last_delivery_id`, repo, d.DeliveredAt, d.ID)
					return err
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
		_ = e.Store.Tx(func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO reconcile_checkpoints(repo,last_delivered_at,last_delivery_id) VALUES(?,?,?) ON CONFLICT(repo) DO UPDATE SET last_delivered_at=excluded.last_delivered_at, last_delivery_id=excluded.last_delivery_id`, repo, detail.DeliveredAt, detail.ID)
			return err
		})
	}
	return nil
}

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/sannrox/rusui/internal/snapshot"
)

type Store struct {
	DB *sql.DB
}

func Open(path string) (*Store, error) {
	// Transactions take the write lock at BEGIN so a second handle on the same
	// file (local attach beside the server) waits in busy_timeout. A deferred
	// read-then-write would get SQLITE_BUSY on lock upgrade without waiting.
	dsn := path + "?_pragma=busy_timeout(5000)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) Tx(fn func(*sql.Tx) error) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

type Job struct {
	ID                  int64
	Repo                string
	Item                int
	ItemKind            string
	Lane                string
	PendingRevision     int
	ClaimedRevision     int
	LeaseGeneration     int
	LeaseExpiresAt      *time.Time
	ExecutionDeadlineAt *time.Time
	RetryCount          int
	State               string
}

func scanJob(row interface{ Scan(...any) error }) (*Job, error) {
	j := &Job{}
	var exp, dead sql.NullString
	if err := row.Scan(&j.ID, &j.Repo, &j.Item, &j.ItemKind, &j.Lane, &j.PendingRevision, &j.ClaimedRevision, &j.LeaseGeneration, &exp, &dead, &j.RetryCount, &j.State); err != nil {
		return nil, err
	}
	if exp.Valid {
		t, _ := time.Parse(time.RFC3339Nano, exp.String)
		j.LeaseExpiresAt = &t
	}
	if dead.Valid {
		t, _ := time.Parse(time.RFC3339Nano, dead.String)
		j.ExecutionDeadlineAt = &t
	}
	return j, nil
}

func GetJobTx(tx *sql.Tx, repo string, item int, lane string) (*Job, error) {
	row := tx.QueryRow(`SELECT id, repo, item, item_kind, lane, pending_revision, claimed_revision, lease_generation, lease_expires_at, execution_deadline_at, retry_count, state FROM jobs WHERE repo=? AND item=? AND lane=?`, repo, item, lane)
	j, err := scanJob(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return j, err
}

func GetJobByIDTx(tx *sql.Tx, id int64) (*Job, error) {
	row := tx.QueryRow(`SELECT id, repo, item, item_kind, lane, pending_revision, claimed_revision, lease_generation, lease_expires_at, execution_deadline_at, retry_count, state FROM jobs WHERE id=?`, id)
	return scanJob(row)
}

func ListDueLeasedJobsTx(tx *sql.Tx, now time.Time, repo, lane string) ([]*Job, error) {
	stamp := now.UTC().Format(time.RFC3339Nano)
	q := `SELECT id, repo, item, item_kind, lane, pending_revision, claimed_revision, lease_generation, lease_expires_at, execution_deadline_at, retry_count, state FROM jobs WHERE state='leased' AND ((lease_expires_at IS NOT NULL AND lease_expires_at<=?) OR (execution_deadline_at IS NOT NULL AND execution_deadline_at<=?))`
	args := []any{stamp, stamp}
	if repo != "" {
		q += ` AND repo=? AND lane=?`
		args = append(args, repo, lane)
	}
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// SetTurnClaimTokenTx records the hash of the claim token that leased a
// turn; an empty hash clears it.
func SetTurnClaimTokenTx(tx *sql.Tx, turnID int64, hash string) error {
	var v any
	if hash != "" {
		v = hash
	}
	_, err := tx.Exec(`UPDATE turns SET claim_token_hash=? WHERE id=?`, v, turnID)
	return err
}

// LeasedTurnByClaimTokenTx returns the leased turn claimed with the token
// hash, or 0 when there is none.
func LeasedTurnByClaimTokenTx(tx *sql.Tx, hash string) (int64, error) {
	if hash == "" {
		return 0, nil
	}
	var id int64
	err := tx.QueryRow(`SELECT id FROM turns WHERE claim_token_hash=? AND state='leased' ORDER BY id DESC LIMIT 1`, hash).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

func UpdateJobTx(tx *sql.Tx, j *Job) error {
	var exp, dead any
	if j.LeaseExpiresAt != nil {
		exp = j.LeaseExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	if j.ExecutionDeadlineAt != nil {
		dead = j.ExecutionDeadlineAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := tx.Exec(`UPDATE turns SET pending_revision=?, claimed_revision=?, lease_generation=?, lease_expires_at=?, execution_deadline_at=?, retry_count=?, state=? WHERE id=?`,
		j.PendingRevision, j.ClaimedRevision, j.LeaseGeneration, exp, dead, j.RetryCount, j.State, j.ID)
	return err
}

func InsertJobTx(tx *sql.Tx, j *Job) error {
	sid, err := ensureReviewSessionTx(tx, j.Repo, j.Item, j.ItemKind)
	if err != nil {
		return err
	}
	res, err := tx.Exec(`INSERT INTO turns (session_id, lane, pending_revision, claimed_revision, lease_generation, retry_count, state) VALUES (?,?,?,?,?,?,?)`,
		sid, j.Lane, j.PendingRevision, j.ClaimedRevision, j.LeaseGeneration, j.RetryCount, j.State)
	if err != nil {
		return err
	}
	j.ID, err = res.LastInsertId()
	return err
}

func ensureReviewSessionTx(tx *sql.Tx, repo string, item int, kind string) (int64, error) {
	var id int64
	err := tx.QueryRow(`SELECT id FROM sessions WHERE kind=? AND repo=? AND item=?`, SessionKindReview, repo, item).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	name := fmt.Sprintf("review-%s-%d", strings.ReplaceAll(repo, "/", "-"), item)
	envRes, err := tx.Exec(`INSERT INTO environments (name, driver, state, created_at) VALUES (?,?,?,?)`,
		name, "process", EnvReady, now)
	if err != nil {
		return 0, err
	}
	envID, err := envRes.LastInsertId()
	if err != nil {
		return 0, err
	}
	res, err := tx.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (?,?,?,?,?,?,?)`,
		envID, SessionKindReview, repo, item, kind, "open", now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func LookupIdempotencyTx(tx *sql.Tx, key string) (int64, bool, error) {
	var id int64
	err := tx.QueryRow(`SELECT session_id FROM session_idempotency WHERE key=?`, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

func SetSessionPromptTx(tx *sql.Tx, sessionID int64, prompt string) error {
	_, err := tx.Exec(`UPDATE sessions SET prompt=? WHERE id=?`, prompt, sessionID)
	return err
}

func SetSessionSizeTx(tx *sql.Tx, sessionID int64, size string) error {
	_, err := tx.Exec(`UPDATE sessions SET size=? WHERE id=?`, size, sessionID)
	return err
}

func SetSessionModeTx(tx *sql.Tx, sessionID int64, mode string) error {
	_, err := tx.Exec(`UPDATE sessions SET mode=? WHERE id=?`, mode, sessionID)
	return err
}

func PutIdempotencyTx(tx *sql.Tx, key string, sessionID int64) error {
	_, err := tx.Exec(`INSERT INTO session_idempotency(key, session_id) VALUES(?,?)`, key, sessionID)
	return err
}

func InsertRunSessionTx(tx *sql.Tx, project, repo, prompt string) (sessionID int64, item int, err error) {
	return insertOperatorSessionTx(tx, SessionKindRun, "run", project, repo, prompt, 0, 0)
}

func InsertChildSessionTx(tx *sql.Tx, project, repo, prompt string, parentID int64) (sessionID int64, item int, err error) {
	return insertOperatorSessionTx(tx, SessionKindRun, "run", project, repo, prompt, 0, parentID)
}

func InsertScheduledSessionTx(tx *sql.Tx, project, repo, prompt string, scheduleID int64) (sessionID int64, item int, err error) {
	return insertOperatorSessionTx(tx, SessionKindScheduled, "scheduled", project, repo, prompt, scheduleID, 0)
}

func insertOperatorSessionTx(tx *sql.Tx, kind, lane, project, repo, prompt string, scheduleID, parentSessionID int64) (sessionID int64, item int, err error) {
	var minItem int
	// Scoped by repo only: item numbers are shared across operator session kinds (run, scheduled, ...).
	if err := tx.QueryRow(`SELECT COALESCE(MIN(item),0) FROM sessions WHERE repo=? AND item<0`, repo).Scan(&minItem); err != nil {
		return 0, 0, err
	}
	item = minItem - 1
	if item >= 0 {
		item = -1
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	name := fmt.Sprintf("%s-%s-%d", kind, strings.ReplaceAll(repo, "/", "-"), item)
	envRes, err := tx.Exec(`INSERT INTO environments (name, driver, state, created_at) VALUES (?,?,?,?)`,
		name, "process", EnvReady, now)
	if err != nil {
		return 0, 0, err
	}
	envID, err := envRes.LastInsertId()
	if err != nil {
		return 0, 0, err
	}
	res, err := tx.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, project, prompt, schedule_id, size, parent_session_id, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		envID, kind, repo, item, kind, "open", project, prompt, scheduleID, "", parentSessionID, now)
	if err != nil {
		return 0, 0, err
	}
	sessionID, err = res.LastInsertId()
	if err != nil {
		return 0, 0, err
	}
	_, err = tx.Exec(`INSERT INTO turns (session_id, lane, pending_revision, claimed_revision, lease_generation, retry_count, state) VALUES (?,?,?,?,?,?,?)`,
		sessionID, lane, 1, 0, 0, 0, "queued")
	return sessionID, item, err
}

func InsertSchedule(s *Store, project, name string, everySeconds int, prompt string, sessionID int64) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO schedules(project, name, every_seconds, prompt, session_id, created_at) VALUES(?,?,?,?,?,?)`,
		project, name, everySeconds, prompt, sessionID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func InsertScheduleTx(tx *sql.Tx, project, name string, everySeconds int, prompt string, sessionID int64, now time.Time) (int64, error) {
	res, err := tx.Exec(`INSERT INTO schedules(project, name, every_seconds, prompt, session_id, created_at) VALUES(?,?,?,?,?,?)`,
		project, name, everySeconds, prompt, sessionID, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func ScheduleByNameTx(tx *sql.Tx, project, name string) (*Schedule, error) {
	var sc Schedule
	err := tx.QueryRow(`SELECT id, project, name, every_seconds, prompt, session_id FROM schedules WHERE project=? AND name=?`, project, name).Scan(
		&sc.ID, &sc.Project, &sc.Name, &sc.EverySeconds, &sc.Prompt, &sc.SessionID)
	if err != nil {
		return nil, err
	}
	return &sc, nil
}

func UpdateScheduleTx(tx *sql.Tx, id int64, everySeconds int, prompt string) error {
	_, err := tx.Exec(`UPDATE schedules SET every_seconds=?, prompt=? WHERE id=?`, everySeconds, prompt, id)
	return err
}

func DeleteScheduleTx(tx *sql.Tx, project string, id int64) error {
	res, err := tx.Exec(`DELETE FROM schedules WHERE id=? AND project=?`, id, project)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("schedule not found")
	}
	return nil
}

func ListSchedules(s *Store) ([]Schedule, error) {
	rows, err := s.DB.Query(`SELECT id, project, name, every_seconds, prompt, session_id FROM schedules`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		var sc Schedule
		if err := rows.Scan(&sc.ID, &sc.Project, &sc.Name, &sc.EverySeconds, &sc.Prompt, &sc.SessionID); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func DeleteSchedule(s *Store, project string, id int64) error {
	res, err := s.DB.Exec(`DELETE FROM schedules WHERE id=? AND project=?`, id, project)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("schedule not found")
	}
	return nil
}

func ScheduleHasLiveSession(tx *sql.Tx, scheduleID int64) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM sessions se JOIN turns t ON t.session_id=se.id WHERE se.schedule_id=? AND t.state IN ('queued','leased')`, scheduleID).Scan(&n)
	return n > 0, err
}

func SaveSnapshotTx(tx *sql.Tx, repo string, item, rev int, it snapshot.Item) error {
	b, err := json.Marshal(it)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO snapshots (repo, item, revision, item_kind, item_hash, main_sha, payload) VALUES (?,?,?,?,?,?,?)`,
		repo, item, rev, it.ItemKind, snapshot.ItemHash(it), it.MainSHA, string(b))
	return err
}

func LoadSnapshotTx(tx *sql.Tx, repo string, item, rev int) (snapshot.Item, error) {
	var payload string
	err := tx.QueryRow(`SELECT payload FROM snapshots WHERE repo=? AND item=? AND revision=?`, repo, item, rev).Scan(&payload)
	if err != nil {
		return snapshot.Item{}, err
	}
	var it snapshot.Item
	if err := json.Unmarshal([]byte(payload), &it); err != nil {
		return snapshot.Item{}, err
	}
	return it, nil
}

// PlaneID is this database's stable plane identity (#406).
func PlaneID(s *Store) (string, error) {
	var id string
	err := s.DB.QueryRow(`SELECT id FROM plane_identity LIMIT 1`).Scan(&id)
	return id, err
}

// ReviewedUpdatedAt maps each item of repo that has a review job to the
// GitHub updated_at of its latest snapshot. Catch-up skips an item whose
// listing is no newer: it already has work for that content (ADR 0038 D5).
func ReviewedUpdatedAt(s *Store, repo string) (map[int]string, error) {
	rows, err := s.DB.Query(`SELECT s.item, IFNULL(json_extract(s.payload, '$.updated_at'), '')
FROM snapshots s
WHERE s.repo=? AND s.revision=(SELECT MAX(x.revision) FROM snapshots x WHERE x.repo=s.repo AND x.item=s.item)
  AND EXISTS (SELECT 1 FROM jobs j WHERE j.repo=s.repo AND j.item=s.item AND j.lane='review')`, repo)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int]string{}
	for rows.Next() {
		var item int
		var at string
		if err := rows.Scan(&item, &at); err != nil {
			return nil, err
		}
		out[item] = at
	}
	return out, rows.Err()
}

func LatestSnapshotHashTx(tx *sql.Tx, repo string, item int) (hash string, rev int, err error) {
	err = tx.QueryRow(`SELECT item_hash, revision FROM snapshots WHERE repo=? AND item=? ORDER BY revision DESC LIMIT 1`, repo, item).Scan(&hash, &rev)
	if err == sql.ErrNoRows {
		return "", 0, nil
	}
	return hash, rev, err
}

func InsertReceiptTx(tx *sql.Tx, jobID int64, gen, claimed int, kind, payload string) error {
	_, err := tx.Exec(`INSERT INTO receipts (job_id, lease_generation, claimed_revision, kind, payload) VALUES (?,?,?,?,?)`,
		jobID, gen, claimed, kind, payload)
	return err
}

func GetReceiptTx(tx *sql.Tx, jobID int64, gen, claimed int) (kind string, payload string, ok bool, err error) {
	err = tx.QueryRow(`SELECT kind, payload FROM receipts WHERE job_id=? AND lease_generation=? AND claimed_revision=?`, jobID, gen, claimed).Scan(&kind, &payload)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return kind, payload, true, nil
}

func OverlayGet(tx *sql.Tx, key string) (string, error) {
	var v string
	err := tx.QueryRow(`SELECT value FROM overlay WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func OverlaySet(tx *sql.Tx, key, value string) error {
	_, err := tx.Exec(`INSERT INTO overlay(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func Paused(tx *sql.Tx, project string) (bool, error) {
	g, err := OverlayGet(tx, "pause:global")
	if err != nil {
		return false, err
	}
	if g == "1" {
		return true, nil
	}
	if project == "" {
		return false, nil
	}
	r, err := OverlayGet(tx, "pause:"+project)
	return r == "1", err
}

func CountReviewsToday(tx *sql.Tx, repo, day string) (int, error) {
	var n int
	err := tx.QueryRow(`SELECT count FROM daily_review_counts WHERE repo=? AND day=?`, repo, day).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return n, err
}

func IncrReviewsToday(tx *sql.Tx, repo, day string) error {
	_, err := tx.Exec(`INSERT INTO daily_review_counts(repo,day,count) VALUES(?,?,1) ON CONFLICT(repo,day) DO UPDATE SET count=count+1`, repo, day)
	return err
}

func DecrReviewsToday(tx *sql.Tx, repo, day string) error {
	res, err := tx.Exec(`UPDATE daily_review_counts SET count=count-1 WHERE repo=? AND day=? AND count>0`, repo, day)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("daily review count missing for %s on %s", repo, day)
	}
	return nil
}

func EnsureRefreshQueuedTx(tx *sql.Tx, repo string, item int, kind string, force bool) error {
	var owner int
	var needs int
	err := tx.QueryRow(`SELECT owner, needs_another FROM refresh_requests WHERE repo=? AND item=?`, repo, item).Scan(&owner, &needs)
	if err == sql.ErrNoRows {
		f := 0
		if force {
			f = 1
		}
		_, err = tx.Exec(`INSERT INTO refresh_requests (repo,item,item_kind,generation,owner,force,state) VALUES (?,?,?,?,0,?, 'queued')`, repo, item, kind, 0, f)
		return err
	}
	if err != nil {
		return err
	}
	if owner != 0 {
		_, err = tx.Exec(`UPDATE refresh_requests SET needs_another=1, force=CASE WHEN ? THEN 1 ELSE force END, state='queued' WHERE repo=? AND item=?`, boolToInt(force), repo, item)
		return err
	}
	_, err = tx.Exec(`UPDATE refresh_requests SET state='queued', force=CASE WHEN ? THEN 1 ELSE force END WHERE repo=? AND item=?`, boolToInt(force), repo, item)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func InsertDeliveryTx(tx *sql.Tx, id, repo string, item int, kind string, at time.Time) (inserted bool, err error) {
	res, err := tx.Exec(`INSERT OR IGNORE INTO deliveries (delivery_id, repo, item, item_kind, received_at) VALUES (?,?,?,?,?)`,
		id, repo, item, kind, at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		var sid sql.NullInt64
		_ = tx.QueryRow(`SELECT id FROM sessions WHERE kind=? AND repo=? AND item=?`, SessionKindReview, repo, item).Scan(&sid)
		var session any
		if sid.Valid {
			session = sid.Int64
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO events (session_id, source, delivery_id, repo, item, item_kind, received_at) VALUES (?,?,?,?,?,?,?)`,
			session, "github", id, repo, item, kind, at.UTC().Format(time.RFC3339Nano)); err != nil {
			return false, err
		}
	}
	return n == 1, nil
}

func InsertEventTx(tx *sql.Tx, id, source, repo string, item int, kind string, at time.Time) (inserted bool, err error) {
	if source == "" {
		source = "generic"
	}
	res, err := tx.Exec(`INSERT OR IGNORE INTO deliveries (delivery_id, repo, item, item_kind, received_at) VALUES (?,?,?,?,?)`,
		id, repo, item, kind, at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		var sid sql.NullInt64
		_ = tx.QueryRow(`SELECT id FROM sessions WHERE kind=? AND repo=? AND item=?`, SessionKindReview, repo, item).Scan(&sid)
		var session any
		if sid.Valid {
			session = sid.Int64
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO events (session_id, source, delivery_id, repo, item, item_kind, received_at) VALUES (?,?,?,?,?,?,?)`,
			session, source, id, repo, item, kind, at.UTC().Format(time.RFC3339Nano)); err != nil {
			return false, err
		}
	}
	return n == 1, nil
}

func CountEvents(s *Store, repo string, item int) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE repo=? AND item=?`, repo, item).Scan(&n)
	return n, err
}

func CountIntended(s *Store, repo string, item int) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM actions WHERE repo=? AND item=?`, repo, item).Scan(&n)
	return n, err
}

func CountReviews(s *Store, jobID int64) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM review_revisions WHERE job_id=?`, jobID).Scan(&n)
	return n, err
}

func CountReceipts(s *Store, jobID int64) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM receipts WHERE job_id=?`, jobID).Scan(&n)
	return n, err
}

func LatestReviewJSON(s *Store, jobID int64) (string, error) {
	var p string
	err := s.DB.QueryRow(`SELECT payload FROM review_revisions WHERE job_id=? ORDER BY id DESC LIMIT 1`, jobID).Scan(&p)
	return p, err
}

// LatestReviewForSession returns the latest immutable review for a session.
func LatestReviewForSession(s *Store, sessionID int64) (revisionID int64, payload string, ok bool, err error) {
	err = s.DB.QueryRow(`SELECT r.id, r.payload FROM review_revisions r JOIN turns t ON t.id=r.job_id WHERE t.session_id=? ORDER BY r.id DESC LIMIT 1`, sessionID).Scan(&revisionID, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	return revisionID, payload, err == nil, err
}

// SessionCancelled reports whether a session is cancelled now: a local
// session marked cancelled, or a managed turn that is failed because it
// was cancelled. A failed turn was cancelled when its newest receipt is
// a cancellation, or when an unclaimed revision is still pending, which
// only cancelling a queued turn leaves behind. A follow-up requeues the
// turn, so an earlier cancellation stops counting.
func SessionCancelled(s *Store, sessionID int64) (bool, error) {
	var cancelled bool
	err := s.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id=? AND state='cancelled')
  OR EXISTS(SELECT 1 FROM turns t WHERE t.session_id=? AND t.state='failed' AND (t.claimed_revision < t.pending_revision
    OR COALESCE((SELECT r.kind FROM receipts r WHERE r.job_id=t.id ORDER BY r.rowid DESC LIMIT 1), '')='cancelled'))`,
		sessionID, sessionID).Scan(&cancelled)
	return cancelled, err
}

func IntendedBodies(s *Store, repo string, item int) ([]string, error) {
	rows, err := s.DB.Query(`SELECT body FROM actions WHERE repo=? AND item=?`, repo, item)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func LoadSnapshot(s *Store, repo string, item, rev int) (snapshot.Item, error) {
	var it snapshot.Item
	err := s.Tx(func(tx *sql.Tx) error {
		var err error
		it, err = LoadSnapshotTx(tx, repo, item, rev)
		return err
	})
	return it, err
}

func JobState(s *Store, repo string, item int) (*Job, error) {
	var j *Job
	err := s.Tx(func(tx *sql.Tx) error {
		var err error
		j, err = GetJobTx(tx, repo, item, "review")
		return err
	})
	return j, err
}

type ApplyTarget struct {
	Repo string
	Item int
}

func ListRetryableApply(s *Store) ([]ApplyTarget, error) {
	rows, err := s.DB.Query(`SELECT DISTINCT repo, item FROM apply_attempts WHERE state IN ('planned','in_flight','uncertain')`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ApplyTarget
	for rows.Next() {
		var t ApplyTarget
		if err := rows.Scan(&t.Repo, &t.Item); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func ListLocalTrackedItems(s *Store) ([][3]any, error) {
	rows, err := s.DB.Query(`SELECT repo, item, item_kind FROM jobs WHERE state IN ('queued','leased','failed')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][3]any
	for rows.Next() {
		var repo, kind, st string
		var item int
		_ = st
		if err := rows.Scan(&repo, &item, &kind); err != nil {
			return nil, err
		}
		out = append(out, [3]any{repo, item, kind})
	}
	return out, rows.Err()
}

func SplitRepo(name string) (owner, repo string, err error) {
	a, b, ok := strings.Cut(name, "/")
	if !ok || a == "" || b == "" {
		return "", "", fmt.Errorf("bad repo %q", name)
	}
	return a, b, nil
}

func PutTurnCredential(s *Store, turnID int64, gen int, tokenHash, expiresAt string) error {
	_, err := s.DB.Exec(`INSERT OR REPLACE INTO turn_credentials (turn_id, lease_generation, token_hash, expires_at) VALUES (?,?,?,?)`,
		turnID, gen, tokenHash, expiresAt)
	return err
}

func RenewTurnCredentialTx(tx *sql.Tx, turnID int64, gen int, expiresAt string) error {
	_, err := tx.Exec(`UPDATE turn_credentials SET expires_at=? WHERE turn_id=? AND lease_generation=?`,
		expiresAt, turnID, gen)
	return err
}

func TouchRunner(s *Store, name string, at time.Time) error {
	_, err := s.DB.Exec(`UPDATE runners SET last_seen_at=?, state='ready' WHERE name=?`, at.UTC().Format(time.RFC3339Nano), name)
	return err
}

func GetTurn(s *Store, id int64) (*Turn, error) {
	var t Turn
	err := s.DB.QueryRow(`SELECT id, session_id, lane, pending_revision, claimed_revision, lease_generation, retry_count, state FROM turns WHERE id=?`, id).Scan(
		&t.ID, &t.SessionID, &t.Lane, &t.PendingRevision, &t.ClaimedRevision, &t.LeaseGeneration, &t.RetryCount, &t.State)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func ListSessions(s *Store, project string, limit int) ([]Session, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT s.id, s.environment_id, e.state, s.kind, s.repo, s.item, s.item_kind, s.state, s.project, s.prompt, s.size, COALESCE(s.mode, ''), s.archived, s.archived_at, COALESCE(s.parent_session_id, 0), s.created_at
FROM sessions s LEFT JOIN environments e ON e.id=s.environment_id`
	args := []any{}
	if project != "" {
		q += ` WHERE s.project=?`
		args = append(args, project)
	}
	q += ` ORDER BY s.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Session
	for rows.Next() {
		var sess Session
		var created string
		var archived int
		var archivedAt sql.NullString
		if err := rows.Scan(&sess.ID, &sess.EnvironmentID, &sess.EnvironmentState, &sess.Kind, &sess.Repo, &sess.Item, &sess.ItemKind, &sess.State, &sess.Project, &sess.Prompt, &sess.Size, &sess.Mode, &archived, &archivedAt, &sess.ParentSessionID, &created); err != nil {
			return nil, err
		}
		if err := applySessionArchive(&sess, archived, archivedAt, created); err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

func ListTurnsForSession(s *Store, sessionID int64) ([]Turn, error) {
	rows, err := s.DB.Query(`SELECT id, session_id, lane, pending_revision, claimed_revision, lease_generation, retry_count, state FROM turns WHERE session_id=? ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Turn
	for rows.Next() {
		var t Turn
		if err := rows.Scan(&t.ID, &t.SessionID, &t.Lane, &t.PendingRevision, &t.ClaimedRevision, &t.LeaseGeneration, &t.RetryCount, &t.State); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func ListActionsForSession(s *Store, sessionID int64) ([]Action, error) {
	rows, err := s.DB.Query(`SELECT action_id, session_id, turn_id, review_revision_id, repo, item, action_type, reason_code, evidence_class, limit_sentence, body FROM actions WHERE session_id=? ORDER BY action_id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Action
	for rows.Next() {
		var a Action
		var sid, tid, rid sql.NullInt64
		if err := rows.Scan(&a.ID, &sid, &tid, &rid, &a.Repo, &a.Item, &a.Type, &a.ReasonCode, &a.EvidenceClass, &a.LimitSentence, &a.Body); err != nil {
			return nil, err
		}
		if sid.Valid {
			a.SessionID = &sid.Int64
		}
		if tid.Valid {
			a.TurnID = &tid.Int64
		}
		if rid.Valid {
			a.ReviewRevisionID = &rid.Int64
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// TranscriptEntry is one durable transcript action with its append
// position. Seq is the actions rowid: SQLite has one writer at a time, so
// rowids grow in commit order and a reader that resumes after Seq misses
// no later entry. Actions are never deleted and Rusui never VACUUMs, so a
// recorded Seq keeps naming the same entry.
type TranscriptEntry struct {
	Seq int64
	Action
}

// ListTranscriptAfter returns at most limit actions of the session
// appended after seq, oldest first.
func ListTranscriptAfter(s *Store, sessionID, seq int64, limit int) ([]TranscriptEntry, error) {
	rows, err := s.DB.Query(`SELECT rowid, action_id, action_type, reason_code, body FROM actions WHERE session_id=? AND rowid>? ORDER BY rowid LIMIT ?`, sessionID, seq, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []TranscriptEntry
	for rows.Next() {
		var e TranscriptEntry
		if err := rows.Scan(&e.Seq, &e.ID, &e.Type, &e.ReasonCode, &e.Body); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FollowToken summarizes everything that can change what a followed read
// shows: the newest transcript entry, each turn's state and revision, the
// session state, and the approval decisions on the session's requests.
// It is one indexed query, so an idle follower can poll it cheaply and
// read the rest only when it changes (#439).
func FollowToken(s *Store, sessionID int64) (string, error) {
	var token string
	err := s.DB.QueryRow(`SELECT
  COALESCE((SELECT MAX(rowid) FROM actions WHERE session_id=?1), 0) || '|' ||
  COALESCE((SELECT group_concat(id || ':' || state || ':' || pending_revision || ':' || claimed_revision, ',') FROM (SELECT id, state, pending_revision, claimed_revision FROM turns WHERE session_id=?1 ORDER BY id)), '') || '|' ||
  COALESCE((SELECT state FROM sessions WHERE id=?1), '') || '|' ||
  (SELECT COUNT(*) FROM actions a JOIN approval_decisions d ON d.action_id=a.action_id WHERE a.session_id=?1 AND a.action_type='acp.approval')`, sessionID).Scan(&token)
	return token, err
}

// CountPendingApprovals is the number of the session's approval requests
// an operator can still answer: unmatched by policy, undecided, and on a
// turn that is leased now. A policy denial needs no answer, and a request
// whose turn ended stays undecided forever.
func CountPendingApprovals(s *Store, sessionID int64) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM actions a
JOIN turns t ON t.id=a.turn_id AND t.state='leased'
LEFT JOIN approval_decisions d ON d.action_id=a.action_id
WHERE a.session_id=? AND a.action_type='acp.approval' AND a.reason_code='permission_unmatched' AND d.action_id IS NULL`, sessionID).Scan(&n)
	return n, err
}

// OldestPendingApproval is the oldest approval request CountPendingApprovals
// counts: its transcript seq, its turn, and the revision that turn runs.
func OldestPendingApproval(s *Store, sessionID int64) (seq, turnID int64, revision int, ok bool, err error) {
	err = s.DB.QueryRow(`SELECT a.rowid, t.id, t.claimed_revision FROM actions a
JOIN turns t ON t.id=a.turn_id AND t.state='leased'
LEFT JOIN approval_decisions d ON d.action_id=a.action_id
WHERE a.session_id=? AND a.action_type='acp.approval' AND a.reason_code='permission_unmatched' AND d.action_id IS NULL
ORDER BY a.rowid LIMIT 1`, sessionID).Scan(&seq, &turnID, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, 0, false, nil
	}
	return seq, turnID, revision, err == nil, err
}

func CountChildSessionsTx(tx *sql.Tx, parentID int64) (int, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE parent_session_id=?`, parentID).Scan(&n)
	return n, err
}

func ListChildSessionIDs(s *Store, parentID int64) ([]int64, error) {
	rows, err := s.DB.Query(`SELECT id FROM sessions WHERE parent_session_id=? ORDER BY id`, parentID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func SessionLineageTx(tx *sql.Tx, sessionID int64) (parentID int64, repo string, item int, err error) {
	err = tx.QueryRow(`SELECT COALESCE(parent_session_id, 0), repo, item FROM sessions WHERE id=?`, sessionID).Scan(&parentID, &repo, &item)
	return parentID, repo, item, err
}

// HasOpenRepoSessionsTx reports whether any non-archived session still
// uses repo as sessions.repo. Claim uses this so a leftover pinless
// session remains claimable after the project binds a repository (#557).
func HasOpenRepoSessionsTx(tx *sql.Tx, repo string) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE repo=? AND COALESCE(archived, 0)=0`, repo).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func GetSession(s *Store, id int64) (*Session, error) {
	var sess Session
	var created string
	var archived int
	var archivedAt sql.NullString
	err := s.DB.QueryRow(`SELECT s.id, s.environment_id, e.state, s.kind, s.repo, s.item, s.item_kind, s.state, s.project, s.prompt, s.guest_session_id, s.size, COALESCE(s.mode, ''), s.archived, s.archived_at, COALESCE(s.parent_session_id, 0), s.created_at
FROM sessions s LEFT JOIN environments e ON e.id=s.environment_id WHERE s.id=?`, id).Scan(
		&sess.ID, &sess.EnvironmentID, &sess.EnvironmentState, &sess.Kind, &sess.Repo, &sess.Item, &sess.ItemKind, &sess.State, &sess.Project, &sess.Prompt, &sess.GuestSessionID, &sess.Size, &sess.Mode, &archived, &archivedAt, &sess.ParentSessionID, &created)
	if err != nil {
		return nil, err
	}
	if err := applySessionArchive(&sess, archived, archivedAt, created); err != nil {
		return nil, err
	}
	return &sess, nil
}

func applySessionArchive(sess *Session, archived int, archivedAt sql.NullString, created string) error {
	sess.Archived = archived != 0
	if archivedAt.Valid && archivedAt.String != "" {
		t, err := time.Parse(time.RFC3339Nano, archivedAt.String)
		if err != nil {
			return err
		}
		sess.ArchivedAt = &t
	}
	if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
		sess.CreatedAt = t
	}
	return nil
}

// SetSessionArchived records or clears archive. The session row stays.
func SetSessionArchived(s *Store, id int64, archived bool, now time.Time) error {
	if archived {
		_, err := s.DB.Exec(`UPDATE sessions SET archived=1, archived_at=? WHERE id=?`,
			now.UTC().Format(time.RFC3339Nano), id)
		return err
	}
	_, err := s.DB.Exec(`UPDATE sessions SET archived=0, archived_at=NULL WHERE id=?`, id)
	return err
}

func RevokePreviewGrantsForEnvironment(s *Store, envID int64) error {
	_, err := s.DB.Exec(`UPDATE preview_grants SET revoked=1 WHERE environment_id=? AND revoked=0`, envID)
	return err
}

func TurnTokenSession(s *Store, tokenHash string, now time.Time) (sessionID, turnID int64, ok bool, err error) {
	var exp string
	err = s.DB.QueryRow(`SELECT t.session_id, t.id, c.expires_at FROM turn_credentials c JOIN turns t ON t.id=c.turn_id WHERE c.token_hash=?`, tokenHash).Scan(&sessionID, &turnID, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	t, err := time.Parse(time.RFC3339Nano, exp)
	if err != nil {
		return 0, 0, false, err
	}
	return sessionID, turnID, now.Before(t), nil
}

func TurnCredentialValid(s *Store, turnID int64, tokenHash string, now time.Time) (bool, error) {
	var exp string
	err := s.DB.QueryRow(`SELECT expires_at FROM turn_credentials WHERE turn_id=? AND token_hash=?`, turnID, tokenHash).Scan(&exp)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	t, err := time.Parse(time.RFC3339Nano, exp)
	if err != nil {
		return false, err
	}
	return now.Before(t), nil
}

func SetSessionEnvironment(s *Store, sessionID, envID int64) error {
	_, err := s.DB.Exec(`UPDATE sessions SET environment_id=? WHERE id=?`, envID, sessionID)
	return err
}

func GetEnvironment(s *Store, id int64) (*Environment, error) {
	row := s.DB.QueryRow(`SELECT id, name, driver, state, handle, source_hash, expires_at, slept_at, cpu_millis, memory_bytes, created_at FROM environments WHERE id=?`, id)
	return scanEnvironment(row)
}

func InsertEnvironment(s *Store, e Environment) (int64, error) {
	var exp, slept any
	if e.ExpiresAt != nil {
		exp = e.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	if e.SleptAt != nil {
		slept = e.SleptAt.UTC().Format(time.RFC3339Nano)
	}
	res, err := s.DB.Exec(`INSERT INTO environments (name, driver, state, handle, source_hash, expires_at, slept_at, cpu_millis, memory_bytes, created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		e.Name, e.Driver, e.State, e.Handle, e.SourceHash, exp, slept, e.CPUMillis, e.MemoryBytes, e.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func UpdateEnvironment(s *Store, e Environment) error {
	var exp, slept any
	if e.ExpiresAt != nil {
		exp = e.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	if e.SleptAt != nil {
		slept = e.SleptAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.DB.Exec(`UPDATE environments SET driver=?, state=?, handle=?, source_hash=?, expires_at=?, slept_at=?, cpu_millis=?, memory_bytes=? WHERE id=?`,
		e.Driver, e.State, e.Handle, e.SourceHash, exp, slept, e.CPUMillis, e.MemoryBytes, e.ID)
	return err
}

func scanEnvironment(row interface{ Scan(...any) error }) (*Environment, error) {
	var e Environment
	var handle, source, exp, slept, created sql.NullString
	if err := row.Scan(&e.ID, &e.Name, &e.Driver, &e.State, &handle, &source, &exp, &slept, &e.CPUMillis, &e.MemoryBytes, &created); err != nil {
		return nil, err
	}
	e.Handle = handle.String
	e.SourceHash = source.String
	if exp.Valid {
		t, err := time.Parse(time.RFC3339Nano, exp.String)
		if err != nil {
			return nil, err
		}
		e.ExpiresAt = &t
	}
	if slept.Valid {
		t, err := time.Parse(time.RFC3339Nano, slept.String)
		if err != nil {
			return nil, err
		}
		e.SleptAt = &t
	}
	if created.Valid {
		t, err := time.Parse(time.RFC3339Nano, created.String)
		if err != nil {
			return nil, err
		}
		e.CreatedAt = t
	}
	return &e, nil
}

func ListPendingApprovals(s *Store) ([]Action, error) {
	rows, err := s.DB.Query(`SELECT a.action_id, a.session_id, a.turn_id, a.review_revision_id, a.repo, a.item, a.action_type, a.reason_code, a.evidence_class, a.limit_sentence, a.body
FROM actions a LEFT JOIN approval_decisions d ON d.action_id=a.action_id
WHERE a.action_type='acp.approval' AND d.action_id IS NULL
ORDER BY a.action_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Action
	for rows.Next() {
		var a Action
		var sid, tid, rid sql.NullInt64
		if err := rows.Scan(&a.ID, &sid, &tid, &rid, &a.Repo, &a.Item, &a.Type, &a.ReasonCode, &a.EvidenceClass, &a.LimitSentence, &a.Body); err != nil {
			return nil, err
		}
		if sid.Valid {
			a.SessionID = &sid.Int64
		}
		if tid.Valid {
			a.TurnID = &tid.Int64
		}
		if rid.Valid {
			a.ReviewRevisionID = &rid.Int64
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func GetAction(s *Store, id string) (*Action, error) {
	var a Action
	var sid, tid, rid sql.NullInt64
	err := s.DB.QueryRow(`SELECT action_id, session_id, turn_id, review_revision_id, repo, item, action_type, reason_code, evidence_class, limit_sentence, body FROM actions WHERE action_id=?`, id).Scan(
		&a.ID, &sid, &tid, &rid, &a.Repo, &a.Item, &a.Type, &a.ReasonCode, &a.EvidenceClass, &a.LimitSentence, &a.Body)
	if err != nil {
		return nil, err
	}
	if sid.Valid {
		a.SessionID = &sid.Int64
	}
	if tid.Valid {
		a.TurnID = &tid.Int64
	}
	if rid.Valid {
		a.ReviewRevisionID = &rid.Int64
	}
	return &a, nil
}

func GetApprovalDecision(s *Store, actionID string) (string, bool, error) {
	var d string
	err := s.DB.QueryRow(`SELECT decision FROM approval_decisions WHERE action_id=?`, actionID).Scan(&d)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return d, true, nil
}

func PutApprovalDecision(s *Store, actionID, decision string) error {
	_, err := s.DB.Exec(`INSERT INTO approval_decisions(action_id, decision, decided_at) VALUES(?,?,?)`,
		actionID, decision, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func InsertAction(s *Store, a Action) error {
	return s.Tx(func(tx *sql.Tx) error {
		return InsertActionTx(tx, a)
	})
}

func InsertActionTx(tx *sql.Tx, a Action) error {
	_, err := tx.Exec(`INSERT INTO actions (action_id, session_id, turn_id, review_revision_id, repo, item, action_type, reason_code, evidence_class, limit_sentence, body) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.SessionID, a.TurnID, a.ReviewRevisionID, a.Repo, a.Item, a.Type, a.ReasonCode, a.EvidenceClass, a.LimitSentence, a.Body)
	return err
}

func ListActions(s *Store, repo string, item int) ([]Action, error) {
	rows, err := s.DB.Query(`SELECT action_id, session_id, turn_id, review_revision_id, repo, item, action_type, reason_code, evidence_class, limit_sentence, body FROM actions WHERE repo=? AND item=? ORDER BY action_id`,
		repo, item)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Action
	for rows.Next() {
		var a Action
		var sid, tid, rid sql.NullInt64
		if err := rows.Scan(&a.ID, &sid, &tid, &rid, &a.Repo, &a.Item, &a.Type, &a.ReasonCode, &a.EvidenceClass, &a.LimitSentence, &a.Body); err != nil {
			return nil, err
		}
		if sid.Valid {
			a.SessionID = &sid.Int64
		}
		if tid.Valid {
			a.TurnID = &tid.Int64
		}
		if rid.Valid {
			a.ReviewRevisionID = &rid.Int64
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

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
	dsn := path + "?_pragma=busy_timeout(5000)"
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

func GetSession(s *Store, id int64) (*Session, error) {
	var sess Session
	var created string
	err := s.DB.QueryRow(`SELECT id, environment_id, kind, repo, item, item_kind, state, created_at FROM sessions WHERE id=?`, id).Scan(
		&sess.ID, &sess.EnvironmentID, &sess.Kind, &sess.Repo, &sess.Item, &sess.ItemKind, &sess.State, &created)
	if err != nil {
		return nil, err
	}
	if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
		sess.CreatedAt = t
	}
	return &sess, nil
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

func HasPreparedSourceHash(s *Store, hash string) (bool, error) {
	if hash == "" {
		return false, nil
	}
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM environments WHERE source_hash=? AND IFNULL(handle,'')!='' AND state!=?`, hash, EnvExpired).Scan(&n)
	return n > 0, err
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
	_, err := s.DB.Exec(`UPDATE environments SET state=?, handle=?, source_hash=?, expires_at=?, slept_at=?, cpu_millis=?, memory_bytes=? WHERE id=?`,
		e.State, e.Handle, e.SourceHash, exp, slept, e.CPUMillis, e.MemoryBytes, e.ID)
	return err
}

func ListExpiredEnvironments(s *Store, now time.Time) ([]Environment, error) {
	rows, err := s.DB.Query(`SELECT id, name, driver, state, handle, source_hash, expires_at, slept_at, cpu_millis, memory_bytes, created_at FROM environments WHERE name!=? AND state!=? AND expires_at IS NOT NULL AND expires_at<=?`,
		LocalEnvironmentName, EnvExpired, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Environment
	for rows.Next() {
		env, err := scanEnvironment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *env)
	}
	return out, rows.Err()
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

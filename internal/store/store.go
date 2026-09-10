package store

import (
	"database/sql"
	"encoding/json"
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

func (s *Store) migrate() error {
	_, err := s.DB.Exec(`
CREATE TABLE IF NOT EXISTS deliveries (
  delivery_id TEXT PRIMARY KEY,
  repo TEXT NOT NULL,
  item INTEGER NOT NULL,
  item_kind TEXT NOT NULL,
  received_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS refresh_requests (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  repo TEXT NOT NULL,
  item INTEGER NOT NULL,
  item_kind TEXT NOT NULL,
  generation INTEGER NOT NULL DEFAULT 0,
  owner INTEGER NOT NULL DEFAULT 0,
  owner_expires_at TEXT,
  retry_count INTEGER NOT NULL DEFAULT 0,
  needs_another INTEGER NOT NULL DEFAULT 0,
  force INTEGER NOT NULL DEFAULT 0,
  state TEXT NOT NULL DEFAULT 'queued',
  not_before TEXT,
  UNIQUE(repo, item)
);
CREATE TABLE IF NOT EXISTS snapshots (
  repo TEXT NOT NULL,
  item INTEGER NOT NULL,
  revision INTEGER NOT NULL,
  item_kind TEXT NOT NULL,
  item_hash TEXT NOT NULL,
  main_sha TEXT NOT NULL,
  payload TEXT NOT NULL,
  PRIMARY KEY (repo, item, revision)
);
CREATE TABLE IF NOT EXISTS jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  repo TEXT NOT NULL,
  item INTEGER NOT NULL,
  item_kind TEXT NOT NULL,
  lane TEXT NOT NULL,
  pending_revision INTEGER NOT NULL DEFAULT 0,
  claimed_revision INTEGER NOT NULL DEFAULT 0,
  lease_generation INTEGER NOT NULL DEFAULT 0,
  lease_expires_at TEXT,
  execution_deadline_at TEXT,
  retry_count INTEGER NOT NULL DEFAULT 0,
  state TEXT NOT NULL DEFAULT 'queued',
  UNIQUE(repo, item, lane)
);
CREATE TABLE IF NOT EXISTS receipts (
  job_id INTEGER NOT NULL,
  lease_generation INTEGER NOT NULL,
  claimed_revision INTEGER NOT NULL,
  kind TEXT NOT NULL,
  payload TEXT,
  PRIMARY KEY (job_id, lease_generation, claimed_revision)
);
CREATE TABLE IF NOT EXISTS review_revisions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id INTEGER NOT NULL,
  claimed_revision INTEGER NOT NULL,
  item_hash TEXT NOT NULL,
  main_sha TEXT NOT NULL,
  payload TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS apply_attempts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  action_id TEXT NOT NULL,
  review_revision_id INTEGER,
  repo TEXT NOT NULL,
  item INTEGER NOT NULL,
  state TEXT NOT NULL,
  UNIQUE(action_id)
);
CREATE TABLE IF NOT EXISTS intended_actions (
  action_id TEXT PRIMARY KEY,
  review_revision_id INTEGER,
  repo TEXT NOT NULL,
  item INTEGER NOT NULL,
  action_type TEXT NOT NULL,
  reason_code TEXT NOT NULL,
  evidence_class TEXT NOT NULL,
  limit_sentence TEXT NOT NULL,
  body TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS evidence_invalidations (
  review_revision_id INTEGER NOT NULL,
  action_id TEXT NOT NULL,
  consumed INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (review_revision_id, action_id)
);
CREATE TABLE IF NOT EXISTS policy_revisions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  hash TEXT NOT NULL,
  payload TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS overlay (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS daily_review_counts (
  repo TEXT NOT NULL,
  day TEXT NOT NULL,
  count INTEGER NOT NULL,
  PRIMARY KEY (repo, day)
);
CREATE TABLE IF NOT EXISTS retry_audit (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id INTEGER NOT NULL,
  actor TEXT,
  ts TEXT NOT NULL,
  pending_revision INTEGER NOT NULL,
  previous_retry_count INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS reconcile_checkpoints (
  repo TEXT PRIMARY KEY,
  last_delivered_at TEXT,
  last_delivery_id TEXT
);
CREATE TABLE IF NOT EXISTS failed_deliveries (
  delivery_id TEXT PRIMARY KEY,
  repo TEXT NOT NULL,
  retries INTEGER NOT NULL
);
`)
	return err
}

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
	_, err := tx.Exec(`UPDATE jobs SET pending_revision=?, claimed_revision=?, lease_generation=?, lease_expires_at=?, execution_deadline_at=?, retry_count=?, state=? WHERE id=?`,
		j.PendingRevision, j.ClaimedRevision, j.LeaseGeneration, exp, dead, j.RetryCount, j.State, j.ID)
	return err
}

func InsertJobTx(tx *sql.Tx, j *Job) error {
	res, err := tx.Exec(`INSERT INTO jobs (repo, item, item_kind, lane, pending_revision, claimed_revision, lease_generation, retry_count, state) VALUES (?,?,?,?,?,?,?,?,?)`,
		j.Repo, j.Item, j.ItemKind, j.Lane, j.PendingRevision, j.ClaimedRevision, j.LeaseGeneration, j.RetryCount, j.State)
	if err != nil {
		return err
	}
	j.ID, err = res.LastInsertId()
	return err
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

func Paused(tx *sql.Tx, repo string) (bool, error) {
	g, err := OverlayGet(tx, "pause:global")
	if err != nil {
		return false, err
	}
	if g == "1" {
		return true, nil
	}
	r, err := OverlayGet(tx, "pause:"+repo)
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
	return n == 1, nil
}

func CountIntended(s *Store, repo string, item int) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM intended_actions WHERE repo=? AND item=?`, repo, item).Scan(&n)
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
	rows, err := s.DB.Query(`SELECT body FROM intended_actions WHERE repo=? AND item=?`, repo, item)
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

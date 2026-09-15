package store

import (
	"database/sql"
	"fmt"
	"time"
)

// CurrentSchema is the latest applied schema_migrations.version.
const CurrentSchema = 7

// V1SchemaSQL is the implicit schema rusui used before versioned
// migrations. Existing operator databases match this text.
const V1SchemaSQL = `
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
`

func (s *Store) migrate() error {
	if _, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL
)`); err != nil {
		return err
	}
	ver, err := currentVersion(s.DB)
	if err != nil {
		return err
	}
	if ver == 0 {
		var n int
		if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='jobs'`).Scan(&n); err != nil {
			return err
		}
		if n == 1 {
			if err := stamp(s.DB, 1); err != nil {
				return err
			}
			ver = 1
		}
	}
	if ver < 1 {
		if _, err := s.DB.Exec(V1SchemaSQL); err != nil {
			return err
		}
		if err := stamp(s.DB, 1); err != nil {
			return err
		}
		ver = 1
	}
	if ver < 2 {
		if err := migrateV2(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 2); err != nil {
			return err
		}
		ver = 2
	}
	if ver < 3 {
		if err := migrateV3(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 3); err != nil {
			return err
		}
		ver = 3
	}
	if ver < 4 {
		if err := migrateV4(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 4); err != nil {
			return err
		}
		ver = 4
	}
	if ver < 5 {
		if err := migrateV5(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 5); err != nil {
			return err
		}
		ver = 5
	}
	if ver < 6 {
		if err := migrateV6(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 6); err != nil {
			return err
		}
		ver = 6
	}
	if ver < 7 {
		if err := migrateV7(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 7); err != nil {
			return err
		}
	}
	return nil
}

func migrateV7(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS approval_decisions (
  action_id TEXT PRIMARY KEY,
  decision TEXT NOT NULL,
  decided_at TEXT NOT NULL
);
`)
	return err
}

func migrateV6(db *sql.DB) error {
	_, err := db.Exec(`
ALTER TABLE sessions ADD COLUMN schedule_id INTEGER NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS schedules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project TEXT NOT NULL,
  name TEXT NOT NULL,
  every_seconds INTEGER NOT NULL,
  prompt TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(project, name)
);
`)
	return err
}

func migrateV5(db *sql.DB) error {
	_, err := db.Exec(`
ALTER TABLE sessions ADD COLUMN project TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN prompt TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS session_idempotency (
  key TEXT PRIMARY KEY,
  session_id INTEGER NOT NULL
);
`)
	return err
}

func migrateV4(db *sql.DB) error {
	_, err := db.Exec(`
ALTER TABLE environments ADD COLUMN handle TEXT;
ALTER TABLE environments ADD COLUMN source_hash TEXT;
ALTER TABLE environments ADD COLUMN expires_at TEXT;
ALTER TABLE environments ADD COLUMN slept_at TEXT;
ALTER TABLE environments ADD COLUMN cpu_millis INTEGER NOT NULL DEFAULT 0;
ALTER TABLE environments ADD COLUMN memory_bytes INTEGER NOT NULL DEFAULT 0;
`)
	return err
}

func migrateV3(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE turn_credentials (
  turn_id INTEGER NOT NULL,
  lease_generation INTEGER NOT NULL,
  token_hash TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  PRIMARY KEY (turn_id, lease_generation)
);
`)
	return err
}

func currentVersion(db *sql.DB) (int, error) {
	var ver int
	err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&ver)
	return ver, err
}

func stamp(db *sql.DB, version int) error {
	_, err := db.Exec(`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
		version, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func migrateV2(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`
CREATE TABLE environments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  driver TEXT NOT NULL,
  state TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE runners (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  kind TEXT NOT NULL,
  state TEXT NOT NULL,
  last_seen_at TEXT,
  created_at TEXT NOT NULL
);
CREATE TABLE sessions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  environment_id INTEGER NOT NULL,
  kind TEXT NOT NULL,
  repo TEXT NOT NULL,
  item INTEGER NOT NULL,
  item_kind TEXT NOT NULL,
  state TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(kind, repo, item)
);
CREATE TABLE turns (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id INTEGER NOT NULL,
  lane TEXT NOT NULL,
  pending_revision INTEGER NOT NULL DEFAULT 0,
  claimed_revision INTEGER NOT NULL DEFAULT 0,
  lease_generation INTEGER NOT NULL DEFAULT 0,
  lease_expires_at TEXT,
  execution_deadline_at TEXT,
  retry_count INTEGER NOT NULL DEFAULT 0,
  state TEXT NOT NULL DEFAULT 'queued',
  UNIQUE(session_id, lane)
);
CREATE TABLE events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id INTEGER,
  source TEXT NOT NULL,
  delivery_id TEXT UNIQUE,
  repo TEXT NOT NULL,
  item INTEGER NOT NULL,
  item_kind TEXT NOT NULL,
  received_at TEXT NOT NULL
);
CREATE TABLE actions (
  action_id TEXT PRIMARY KEY,
  session_id INTEGER,
  turn_id INTEGER,
  review_revision_id INTEGER,
  repo TEXT NOT NULL,
  item INTEGER NOT NULL,
  action_type TEXT NOT NULL,
  reason_code TEXT NOT NULL,
  evidence_class TEXT NOT NULL,
  limit_sentence TEXT NOT NULL,
  body TEXT NOT NULL
);
INSERT INTO environments (id, name, driver, state, created_at) VALUES (1, 'local', 'process', 'ready', ?);
INSERT INTO runners (id, name, kind, state, created_at) VALUES (1, 'local', 'process', 'ready', ?);
`, now, now); err != nil {
		return fmt.Errorf("v2 tables: %w", err)
	}
	if _, err := tx.Exec(`
INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at)
SELECT DISTINCT 1, 'review', repo, item, item_kind, 'open', ? FROM jobs;
INSERT INTO turns (id, session_id, lane, pending_revision, claimed_revision, lease_generation, lease_expires_at, execution_deadline_at, retry_count, state)
SELECT j.id, s.id, j.lane, j.pending_revision, j.claimed_revision, j.lease_generation, j.lease_expires_at, j.execution_deadline_at, j.retry_count, j.state
FROM jobs j JOIN sessions s ON s.kind='review' AND s.repo=j.repo AND s.item=j.item;
INSERT INTO events (session_id, source, delivery_id, repo, item, item_kind, received_at)
SELECT s.id, 'github', d.delivery_id, d.repo, d.item, d.item_kind, d.received_at
FROM deliveries d LEFT JOIN sessions s ON s.kind='review' AND s.repo=d.repo AND s.item=d.item;
INSERT INTO actions (action_id, session_id, turn_id, review_revision_id, repo, item, action_type, reason_code, evidence_class, limit_sentence, body)
SELECT ia.action_id, s.id, r.job_id, ia.review_revision_id, ia.repo, ia.item, ia.action_type, ia.reason_code, ia.evidence_class, ia.limit_sentence, ia.body
FROM intended_actions ia
LEFT JOIN sessions s ON s.kind='review' AND s.repo=ia.repo AND s.item=ia.item
LEFT JOIN review_revisions r ON r.id=ia.review_revision_id;
`, now); err != nil {
		return fmt.Errorf("v2 copy: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE jobs`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE intended_actions`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
CREATE VIEW jobs AS
SELECT t.id, s.repo, s.item, s.item_kind, t.lane, t.pending_revision, t.claimed_revision,
       t.lease_generation, t.lease_expires_at, t.execution_deadline_at, t.retry_count, t.state
FROM turns t JOIN sessions s ON s.id=t.session_id;
CREATE VIEW intended_actions AS
SELECT action_id, review_revision_id, repo, item, action_type, reason_code, evidence_class, limit_sentence, body
FROM actions;
`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO sqlite_sequence(name, seq) SELECT 'turns', IFNULL(MAX(id), 0) FROM turns`); err != nil {
		return err
	}
	return tx.Commit()
}

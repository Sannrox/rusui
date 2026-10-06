package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// CurrentSchema is the latest applied schema_migrations.version.
const CurrentSchema = 39

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
		ver = 7
	}
	if ver < 8 {
		if err := migrateV8(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 8); err != nil {
			return err
		}
		ver = 8
	}
	if ver < 9 {
		if err := migrateV9(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 9); err != nil {
			return err
		}
		ver = 9
	}
	if ver < 10 {
		if err := migrateV10(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 10); err != nil {
			return err
		}
		ver = 10
	}
	if ver < 11 {
		if err := migrateV11(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 11); err != nil {
			return err
		}
		ver = 11
	}
	if ver < 12 {
		if err := migrateV12(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 12); err != nil {
			return err
		}
		ver = 12
	}
	if ver < 13 {
		if err := migrateV13(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 13); err != nil {
			return err
		}
		ver = 13
	}
	if ver < 14 {
		if err := migrateV14(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 14); err != nil {
			return err
		}
		ver = 14
	}
	if ver < 15 {
		if err := migrateV15(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 15); err != nil {
			return err
		}
		ver = 15
	}
	if ver < 16 {
		if err := migrateV16(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 16); err != nil {
			return err
		}
		ver = 16
	}
	if ver < 17 {
		if err := migrateV17(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 17); err != nil {
			return err
		}
		ver = 17
	}
	if ver < 18 {
		if err := migrateV18(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 18); err != nil {
			return err
		}
		ver = 18
	}
	if ver < 19 {
		if err := migrateV19(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 19); err != nil {
			return err
		}
		ver = 19
	}
	if ver < 20 {
		if err := migrateV20(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 20); err != nil {
			return err
		}
		ver = 20
	}
	if ver < 21 {
		if err := migrateV21(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 21); err != nil {
			return err
		}
	}
	if ver < 22 {
		if err := migrateV22(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 22); err != nil {
			return err
		}
		ver = 22
	}
	if ver < 23 {
		if err := migrateV23(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 23); err != nil {
			return err
		}
	}
	if ver < 24 {
		if err := migrateV24(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 24); err != nil {
			return err
		}
		ver = 24
	}
	if ver < 25 {
		if err := migrateV25(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 25); err != nil {
			return err
		}
		ver = 25
	}
	if ver < 26 {
		if err := migrateV26(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 26); err != nil {
			return err
		}
		ver = 26
	}
	if ver < 27 {
		if err := migrateV27(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 27); err != nil {
			return err
		}
		ver = 27
	}
	if ver < 28 {
		if err := migrateV28(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 28); err != nil {
			return err
		}
		ver = 28
	}
	if ver < 29 {
		if err := migrateV29(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 29); err != nil {
			return err
		}
		ver = 29
	}
	if ver < 30 {
		if err := migrateV30(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 30); err != nil {
			return err
		}
		ver = 30
	}
	if ver < 31 {
		if err := migrateV31(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 31); err != nil {
			return err
		}
	}
	if ver < 32 {
		if err := migrateV32(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 32); err != nil {
			return err
		}
	}
	if ver < 33 {
		if err := migrateV33(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 33); err != nil {
			return err
		}
	}
	if ver < 34 {
		if err := migrateV34(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 34); err != nil {
			return err
		}
	}
	if ver < 35 {
		if err := migrateV35(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 35); err != nil {
			return err
		}
	}
	if ver < 36 {
		if err := migrateV36(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 36); err != nil {
			return err
		}
	}
	if ver < 37 {
		if err := migrateV37(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 37); err != nil {
			return err
		}
	}
	if ver < 38 {
		if err := migrateV38(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 38); err != nil {
			return err
		}
	}
	if ver < 39 {
		if err := migrateV39(s.DB); err != nil {
			return err
		}
		if err := stamp(s.DB, 39); err != nil {
			return err
		}
	}
	return nil
}

// migrateV39 lets a session own a signed webhook (ADR 0070, #510).
func migrateV39(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS session_webhooks (
  session_id INTEGER PRIMARY KEY,
  secret TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS session_webhook_deliveries (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id INTEGER NOT NULL,
  delivery_id TEXT NOT NULL,
  accepted INTEGER NOT NULL,
  detail TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  UNIQUE(session_id, delivery_id)
);
`)
	return err
}

// migrateV38 lets a schedule bind to one session (ADR 0069, #506).
func migrateV38(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('schedules') WHERE name='session_id'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`ALTER TABLE schedules ADD COLUMN session_id INTEGER NOT NULL DEFAULT 0`)
	return err
}

// migrateV37 lets environment captures store plane pre-clone and pre-setup
// output (#504). SQLite cannot alter a CHECK, so the table is rebuilt.
func migrateV37(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
CREATE TABLE environment_captures_v37 (
  environment_id INTEGER NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('setup', 'resume', 'service', 'pre-clone', 'pre-setup')),
  name TEXT NOT NULL DEFAULT '',
  output BLOB NOT NULL,
  truncated INTEGER NOT NULL DEFAULT 0,
  failed INTEGER NOT NULL DEFAULT 0,
  recorded_at TEXT NOT NULL,
  PRIMARY KEY (environment_id, kind, name)
);
INSERT INTO environment_captures_v37 (environment_id, kind, name, output, truncated, failed, recorded_at)
  SELECT environment_id, kind, name, output, truncated, failed, recorded_at FROM environment_captures;
DROP TABLE environment_captures;
ALTER TABLE environment_captures_v37 RENAME TO environment_captures;
`); err != nil {
		return err
	}
	return tx.Commit()
}

// migrateV36 lets environment receipts name an injected secret id (#503).
// SQLite cannot alter a CHECK, so the table is rebuilt.
func migrateV36(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
CREATE TABLE environment_receipts_v36 (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  environment_id INTEGER NOT NULL,
  session_id INTEGER,
  kind TEXT NOT NULL CHECK (kind IN ('sleep', 'wake', 'expire', 'replace', 'secret')),
  state TEXT NOT NULL CHECK (state IN ('succeeded', 'failed')),
  detail TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
INSERT INTO environment_receipts_v36 (id, environment_id, session_id, kind, state, detail, created_at)
  SELECT id, environment_id, session_id, kind, state, detail, created_at FROM environment_receipts;
DROP TABLE environment_receipts;
ALTER TABLE environment_receipts_v36 RENAME TO environment_receipts;
CREATE INDEX IF NOT EXISTS environment_receipts_session ON environment_receipts(session_id, id);
`); err != nil {
		return err
	}
	return tx.Commit()
}

// migrateV35 records the session size name (ADR 0064, #501). Empty means
// the project default, then medium.
func migrateV35(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='size'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`ALTER TABLE sessions ADD COLUMN size TEXT NOT NULL DEFAULT ''`)
	return err
}

// migrateV34 records whether the operator archived the session (#499).
// Archive sleeps the environment and refuses prompts; the row stays.
func migrateV34(db *sql.DB) error {
	for _, col := range []struct {
		name string
		ddl  string
	}{
		{"archived", "ALTER TABLE sessions ADD COLUMN archived INTEGER NOT NULL DEFAULT 0"},
		{"archived_at", "ALTER TABLE sessions ADD COLUMN archived_at TEXT"},
	} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name=?`, col.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := db.Exec(col.ddl); err != nil {
				return err
			}
		}
	}
	return nil
}

// migrateV33 marks follow-ups the operator queued until the current turn
// ends (#492). A queued prompt that has not started can be dropped; the
// dropped row stays so its sequence number is not reused.
func migrateV33(db *sql.DB) error {
	for _, col := range []string{"queued", "dropped"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('followup_queue') WHERE name=?`, col).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := db.Exec(`ALTER TABLE followup_queue ADD COLUMN ` + col + ` INTEGER NOT NULL DEFAULT 0`); err != nil {
				return err
			}
		}
	}
	return nil
}

// migrateV32 records which runner claim token leased a turn (#468), so a
// runner whose claim response was lost can retry with the same token and
// receive the lease it was already granted.
func migrateV32(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('turns') WHERE name='claim_token_hash'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := db.Exec(`ALTER TABLE turns ADD COLUMN claim_token_hash TEXT`); err != nil {
			return err
		}
	}
	_, err := db.Exec(`CREATE INDEX IF NOT EXISTS turns_claim_token ON turns(claim_token_hash)`)
	return err
}

// migrateV31 indexes the transcript by session (#430). A followed read
// asks on every poll for a session's actions after a rowid cursor and for
// its open approvals; the index keeps both lookups proportional to the
// matching rows, not to the table or the session's whole transcript.
func migrateV31(db *sql.DB) error {
	_, err := db.Exec(`
CREATE INDEX IF NOT EXISTS actions_session ON actions(session_id);
CREATE INDEX IF NOT EXISTS actions_session_type ON actions(session_id, action_type);
`)
	return err
}

// migrateV30 lets environment receipts record expiry and replacement
// (#415). SQLite cannot alter a CHECK, so the table is rebuilt; the
// rebuild and its version stamp commit together or not at all.
func migrateV30(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
CREATE TABLE environment_receipts_v30 (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  environment_id INTEGER NOT NULL,
  session_id INTEGER,
  kind TEXT NOT NULL CHECK (kind IN ('sleep', 'wake', 'expire', 'replace')),
  state TEXT NOT NULL CHECK (state IN ('succeeded', 'failed')),
  detail TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
INSERT INTO environment_receipts_v30 (id, environment_id, session_id, kind, state, detail, created_at)
  SELECT id, environment_id, session_id, kind, state, detail, created_at FROM environment_receipts;
DROP TABLE environment_receipts;
ALTER TABLE environment_receipts_v30 RENAME TO environment_receipts;
CREATE INDEX IF NOT EXISTS environment_receipts_session ON environment_receipts(session_id, id);
`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (30, ?)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

// migrateV29 records local lifecycle transitions (#414). session_processes
// and process_attaches keep only the latest state; these rows are the
// append-only history, written in the transaction that changes the state.
func migrateV29(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS process_receipts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id INTEGER NOT NULL,
  process_id INTEGER,
  process_generation INTEGER,
  attach_generation INTEGER,
  kind TEXT NOT NULL CHECK (kind IN ('start', 'observe', 'cancel_requested', 'cancel', 'attach', 'steal', 'detach', 'exit')),
  state TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS process_receipts_session ON process_receipts(session_id, id);
`)
	return err
}

// migrateV28 gives the database a stable plane identity (#406). Guest
// container names carry it, so two planes, or a replaced database, on one
// host never pick the same name.
func migrateV28(db *sql.DB) error {
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS plane_identity (id TEXT NOT NULL);
INSERT INTO plane_identity (id) SELECT ? WHERE NOT EXISTS (SELECT 1 FROM plane_identity);
`, hex.EncodeToString(raw))
	return err
}

// migrateV27 stores maintainer dispositions of review results (ADR 0038
// D8, #379). Rows are append-only; the latest per result counts.
func migrateV27(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS result_dispositions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  review_revision_id INTEGER NOT NULL,
  disposition TEXT NOT NULL CHECK (disposition IN ('useful', 'neutral', 'harmful')),
  wrong_finding INTEGER NOT NULL DEFAULT 0,
  note TEXT NOT NULL DEFAULT '',
  recorded_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS result_dispositions_revision ON result_dispositions(review_revision_id, id);
`)
	return err
}

// migrateV26 stores the last setup, resume, and service output per
// environment (#334). One row per kind and service name; a new run
// replaces it.
func migrateV26(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS environment_captures (
  environment_id INTEGER NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('setup', 'resume', 'service')),
  name TEXT NOT NULL DEFAULT '',
  output BLOB NOT NULL,
  truncated INTEGER NOT NULL DEFAULT 0,
  failed INTEGER NOT NULL DEFAULT 0,
  recorded_at TEXT NOT NULL,
  PRIMARY KEY (environment_id, kind, name)
);
`)
	return err
}

func migrateV25(db *sql.DB) error {
	_, err := db.Exec(`
CREATE INDEX IF NOT EXISTS turns_leased_lease_expires ON turns(state, lease_expires_at);
CREATE INDEX IF NOT EXISTS turns_leased_exec_deadline ON turns(state, execution_deadline_at);
`)
	return err
}

func migrateV24(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS turn_measurements (
  turn_id INTEGER PRIMARY KEY,
  session_id INTEGER NOT NULL,
  project TEXT NOT NULL,
  provider TEXT NOT NULL DEFAULT '',
  provider_version TEXT NOT NULL DEFAULT '',
  terminal_state TEXT NOT NULL DEFAULT '',
  started_at TEXT,
  ended_at TEXT,
  duration_ms INTEGER,
  first_event_ms INTEGER,
  wake_ms INTEGER,
  resume TEXT NOT NULL DEFAULT '',
  permission_decision TEXT NOT NULL DEFAULT '',
  deny_count INTEGER NOT NULL DEFAULT 0,
  tokens_in INTEGER,
  tokens_out INTEGER,
  publication TEXT NOT NULL DEFAULT '',
  exported INTEGER NOT NULL DEFAULT 0
)`)
	return err
}

func migrateV23(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.Query(`PRAGMA table_info(turn_steers)`)
	if err != nil {
		return err
	}
	hasFollowUpSeq := false
	for rows.Next() {
		var cid, notnull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notnull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "followup_seq" {
			hasFollowUpSeq = true
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !hasFollowUpSeq {
		if _, err := tx.Exec(`ALTER TABLE turn_steers ADD COLUMN followup_seq INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}

	rows, err = tx.Query(`SELECT id, session_id FROM turn_steers
WHERE followup_seq=0 AND acknowledged=0 AND promoted=0 ORDER BY id`)
	if err != nil {
		return err
	}
	type pendingSteer struct {
		id        int64
		sessionID int64
	}
	var pending []pendingSteer
	for rows.Next() {
		var steer pendingSteer
		if err := rows.Scan(&steer.id, &steer.sessionID); err != nil {
			_ = rows.Close()
			return err
		}
		pending = append(pending, steer)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, steer := range pending {
		seq, err := nextFollowUpSeqTx(tx, steer.sessionID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE turn_steers SET followup_seq=? WHERE id=?`, seq, steer.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func migrateV22(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(turn_steers)`)
	if err != nil {
		return err
	}
	hasOffered, hasReceived := false, false
	for rows.Next() {
		var cid, notnull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notnull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "offered" {
			hasOffered = true
		}
		if name == "received" {
			hasReceived = true
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !hasOffered {
		if _, err := db.Exec(`ALTER TABLE turn_steers ADD COLUMN offered INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if !hasReceived {
		if _, err := db.Exec(`ALTER TABLE turn_steers ADD COLUMN received INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS turn_steers_unreceived ON turn_steers(turn_id, lease_generation, acknowledged, promoted, received, id)`)
	return err
}

func migrateV21(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS turn_steers (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id INTEGER NOT NULL,
  turn_id INTEGER NOT NULL,
  lease_generation INTEGER NOT NULL,
  prompt TEXT NOT NULL,
  acknowledged INTEGER NOT NULL DEFAULT 0,
  promoted INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS turn_steers_pending ON turn_steers(turn_id, lease_generation, acknowledged, promoted, id);
`)
	return err
}

func migrateV20(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS environment_receipts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  environment_id INTEGER NOT NULL,
  session_id INTEGER,
  kind TEXT NOT NULL CHECK (kind IN ('sleep', 'wake')),
  state TEXT NOT NULL CHECK (state IN ('succeeded', 'failed')),
  detail TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS environment_receipts_session ON environment_receipts(session_id, id);
`)
	return err
}

func migrateV18(db *sql.DB) error {
	_, err := db.Exec(`ALTER TABLE session_processes ADD COLUMN cancel_requested_at TEXT`)
	return err
}

func migrateV19(db *sql.DB) error {
	// Legacy rows have no launch identity; reconciliation fails closed on their empty fingerprint.
	_, err := db.Exec(`ALTER TABLE session_processes ADD COLUMN identity_hash TEXT NOT NULL DEFAULT ''`)
	return err
}

func migrateV17(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS session_processes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id INTEGER NOT NULL,
  generation INTEGER NOT NULL CHECK (generation > 0),
  runtime TEXT NOT NULL CHECK (runtime = 'sumika'),
  name TEXT NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('starting', 'running', 'idle', 'blocked', 'dead', 'lost', 'unknown')),
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at TEXT NOT NULL,
  observed_at TEXT,
  UNIQUE(session_id, generation)
);
CREATE INDEX IF NOT EXISTS session_processes_session ON session_processes(session_id, generation);
CREATE UNIQUE INDEX IF NOT EXISTS session_processes_one_active
  ON session_processes(session_id)
  WHERE state IN ('starting', 'running', 'idle', 'blocked', 'unknown');
CREATE TABLE IF NOT EXISTS process_attaches (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  process_id INTEGER NOT NULL,
  process_generation INTEGER NOT NULL CHECK (process_generation > 0),
  generation INTEGER NOT NULL CHECK (generation > 0),
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
  state TEXT NOT NULL CHECK (state IN ('attached', 'detached', 'stolen', 'process_exited', 'unknown')),
  created_at TEXT NOT NULL,
  observed_at TEXT,
  UNIQUE(process_id, generation)
);
CREATE INDEX IF NOT EXISTS process_attaches_process ON process_attaches(process_id, generation);
CREATE UNIQUE INDEX IF NOT EXISTS process_attaches_one_active
  ON process_attaches(process_id)
  WHERE state = 'attached';
`)
	return err
}

func migrateV16(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS effort_feedback (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  effort_key TEXT NOT NULL,
  seq INTEGER NOT NULL,
  task_id INTEGER NOT NULL,
  candidate_sha TEXT NOT NULL,
  prompt_hash TEXT NOT NULL,
  prompt TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(effort_key, seq)
);
CREATE INDEX IF NOT EXISTS effort_feedback_effort ON effort_feedback(effort_key);
`)
	return err
}

func migrateV15(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS publication_attempts (
  action_id TEXT PRIMARY KEY,
  repo TEXT NOT NULL,
  item INTEGER NOT NULL,
  candidate_sha TEXT NOT NULL,
  source_hash TEXT NOT NULL,
  ref TEXT NOT NULL,
  action TEXT NOT NULL,
  proof_id INTEGER NOT NULL,
  state TEXT NOT NULL,
  pr_number INTEGER NOT NULL DEFAULT 0,
  pr_head_sha TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  intent TEXT NOT NULL,
  policy_hash TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS publication_attempts_state ON publication_attempts(state);
`)
	return err
}

func migrateV14(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS preview_grants (
  token_hash TEXT PRIMARY KEY,
  session_id INTEGER NOT NULL,
  environment_id INTEGER NOT NULL,
  handle TEXT NOT NULL,
  port INTEGER NOT NULL,
  expires_at TEXT NOT NULL,
  revoked INTEGER NOT NULL DEFAULT 0
);
`)
	return err
}

func migrateV13(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS terminal_leases (
  environment_id INTEGER PRIMARY KEY,
  session_id INTEGER NOT NULL,
  generation INTEGER NOT NULL,
  expires_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS terminal_access (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  environment_id INTEGER NOT NULL,
  session_id INTEGER NOT NULL,
  action TEXT NOT NULL,
  detail TEXT NOT NULL,
  created_at TEXT NOT NULL
);
`)
	return err
}

func migrateV12(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS independent_reviews (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  proof_id INTEGER NOT NULL,
  author_session_id INTEGER NOT NULL,
  reviewer_session_id INTEGER NOT NULL,
  candidate_sha TEXT NOT NULL,
  disposition TEXT NOT NULL,
  findings TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS independent_reviews_proof ON independent_reviews(proof_id);
`)
	return err
}

func migrateV11(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS proofs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  candidate_sha TEXT NOT NULL,
  source_hash TEXT NOT NULL,
  log_digest TEXT NOT NULL,
  command TEXT NOT NULL,
  exit_code INTEGER NOT NULL,
  outcome TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS proofs_candidate ON proofs(candidate_sha);
`)
	return err
}

func migrateV10(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS tasks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  effort_key TEXT NOT NULL,
  revision INTEGER NOT NULL,
  spec_hash TEXT NOT NULL,
  session_id INTEGER NOT NULL,
  repo TEXT NOT NULL,
  ref TEXT NOT NULL,
  base_sha TEXT NOT NULL,
  policy_hash TEXT NOT NULL,
  allowed_paths TEXT NOT NULL,
  context_refs TEXT NOT NULL,
  prompt TEXT NOT NULL,
  budget_repairs INTEGER NOT NULL DEFAULT 3,
  state TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(effort_key, revision),
  UNIQUE(spec_hash)
);
CREATE INDEX IF NOT EXISTS tasks_effort ON tasks(effort_key);
`)
	return err
}

func migrateV9(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS credential_grants (
  token_hash TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  session_id INTEGER NOT NULL DEFAULT 0,
  turn_id INTEGER NOT NULL DEFAULT 0,
  repo TEXT NOT NULL,
  can_push INTEGER NOT NULL DEFAULT 0,
  expires_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS credential_grants_turn ON credential_grants(turn_id);
`)
	return err
}

func migrateV8(db *sql.DB) error {
	_, err := db.Exec(`
ALTER TABLE sessions ADD COLUMN guest_session_id TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS followup_queue (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id INTEGER NOT NULL,
  seq INTEGER NOT NULL,
  prompt TEXT NOT NULL,
  consumed INTEGER NOT NULL DEFAULT 0,
  UNIQUE(session_id, seq)
);
`)
	return err
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

package store

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/sannrox/rusui/internal/store/storetest"
)

func TestV1DatabaseUpgradesInPlace(t *testing.T) {
	storetest.SQLiteOnly(t, "SQLite migration steps; PostgreSQL starts at the baseline")
	dir := t.TempDir()
	path := filepath.Join(dir, "v1.db")
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(V1SchemaSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO jobs (id, repo, item, item_kind, lane, pending_revision, claimed_revision, lease_generation, retry_count, state)
		VALUES (7, 'Sannrox/rusui', 42, 'issue', 'review', 3, 3, 2, 1, 'completed')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO receipts (job_id, lease_generation, claimed_revision, kind, payload) VALUES (7, 2, 3, 'complete', '{"ok":true}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO review_revisions (id, job_id, claimed_revision, item_hash, main_sha, payload) VALUES (11, 7, 3, 'hash', 'abc', '{"decision":"keep"}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO intended_actions (action_id, review_revision_id, repo, item, action_type, reason_code, evidence_class, limit_sentence, body)
		VALUES ('act-1', 11, 'Sannrox/rusui', 42, 'comment', 'keep', 'public', 'limit', 'dry-run comment')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO deliveries (delivery_id, repo, item, item_kind, received_at) VALUES ('del-1', 'Sannrox/rusui', 42, 'issue', ?)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO policy_revisions (id, hash, payload) VALUES (3, 'pol-v1', ?)`, "version: 2\nprojects: {}\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO daily_review_counts (repo, day, count) VALUES ('Sannrox/rusui', '2026-01-01', 3)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	var ver int
	if err := s.DB.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&ver); err != nil {
		t.Fatal(err)
	}
	if ver != CurrentSchema {
		t.Fatalf("schema version %d, want %d", ver, CurrentSchema)
	}
	var schedSessionCol int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('schedules') WHERE name='session_id'`).Scan(&schedSessionCol); err != nil || schedSessionCol != 1 {
		t.Fatalf("schedules.session_id missing after upgrade: %d %v", schedSessionCol, err)
	}
	var sessionHooks int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='session_webhooks'`).Scan(&sessionHooks); err != nil || sessionHooks != 1 {
		t.Fatalf("session_webhooks missing after upgrade: %d %v", sessionHooks, err)
	}
	var promptAtt int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='prompt_attachments'`).Scan(&promptAtt); err != nil || promptAtt != 1 {
		t.Fatalf("prompt_attachments missing after upgrade: %d %v", promptAtt, err)
	}
	var parentCol int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='parent_session_id'`).Scan(&parentCol); err != nil || parentCol != 1 {
		t.Fatalf("sessions.parent_session_id missing after upgrade: %d %v", parentCol, err)
	}
	var modeCol int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='mode'`).Scan(&modeCol); err != nil || modeCol != 1 {
		t.Fatalf("sessions.mode missing after upgrade: %d %v", modeCol, err)
	}
	var tenantCol int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='tenant_id'`).Scan(&tenantCol); err != nil || tenantCol != 1 {
		t.Fatalf("sessions.tenant_id missing after upgrade: %d %v", tenantCol, err)
	}
	var tenant string
	if err := s.DB.QueryRow(`SELECT tenant_id FROM sessions LIMIT 1`).Scan(&tenant); err != nil || tenant != DefaultTenantID {
		t.Fatalf("migrated session tenant %q %v", tenant, err)
	}
	var createdByCol int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='created_by'`).Scan(&createdByCol); err != nil || createdByCol != 1 {
		t.Fatalf("sessions.created_by missing after upgrade: %d %v", createdByCol, err)
	}
	var createdBy string
	if err := s.DB.QueryRow(`SELECT created_by FROM sessions LIMIT 1`).Scan(&createdBy); err != nil || createdBy != DefaultTenantID {
		t.Fatalf("migrated session created_by %q %v", createdBy, err)
	}
	var grants int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='session_access_grants'`).Scan(&grants); err != nil || grants != 1 {
		t.Fatalf("session_access_grants missing after upgrade: %d %v", grants, err)
	}
	var commentIDCol int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('apply_attempts') WHERE name='github_comment_id'`).Scan(&commentIDCol); err != nil || commentIDCol != 1 {
		t.Fatalf("apply_attempts.github_comment_id missing after upgrade: %d %v", commentIDCol, err)
	}
	var execCol int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='execution_mode'`).Scan(&execCol); err != nil || execCol != 1 {
		t.Fatalf("sessions.execution_mode missing after upgrade: %d %v", execCol, err)
	}
	var execMode string
	if err := s.DB.QueryRow(`SELECT execution_mode FROM sessions LIMIT 1`).Scan(&execMode); err != nil || execMode != ExecutionSpace {
		t.Fatalf("migrated session execution_mode %q %v", execMode, err)
	}
	var toolGrants int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='session_tool_grants'`).Scan(&toolGrants); err != nil || toolGrants != 1 {
		t.Fatalf("session_tool_grants missing after upgrade: %d %v", toolGrants, err)
	}
	var retention int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='session_retention'`).Scan(&retention); err != nil || retention != 1 {
		t.Fatalf("session_retention missing after upgrade: %d %v", retention, err)
	}
	var retClass string
	if err := s.DB.QueryRow(`SELECT class FROM session_retention LIMIT 1`).Scan(&retClass); err != nil || retClass != RetentionShort {
		t.Fatalf("migrated session retention %q %v", retClass, err)
	}
	var attPath int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('prompt_attachments') WHERE name='path'`).Scan(&attPath); err != nil || attPath != 1 {
		t.Fatalf("prompt_attachments.path missing after upgrade: %d %v", attPath, err)
	}
	var pubs int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='publication_attempts'`).Scan(&pubs); err != nil || pubs != 1 {
		t.Fatalf("publication_attempts missing after upgrade: %d %v", pubs, err)
	}
	var fb int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='effort_feedback'`).Scan(&fb); err != nil || fb != 1 {
		t.Fatalf("effort_feedback missing after upgrade: %d %v", fb, err)
	}
	var haltEvents int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='halt_events'`).Scan(&haltEvents); err != nil || haltEvents != 1 {
		t.Fatalf("halt_events missing after upgrade: %d %v", haltEvents, err)
	}
	var previewServiceCol int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('preview_grants') WHERE name='service_name'`).Scan(&previewServiceCol); err != nil || previewServiceCol != 1 {
		t.Fatalf("preview_grants.service_name missing after upgrade: %d %v", previewServiceCol, err)
	}
	var envServices int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='environment_services'`).Scan(&envServices); err != nil || envServices != 1 {
		t.Fatalf("environment_services missing after upgrade: %d %v", envServices, err)
	}

	j, err := JobState(s, "Sannrox/rusui", 42)
	if err != nil {
		t.Fatal(err)
	}
	if j == nil || j.ID != 7 || j.State != "completed" || j.PendingRevision != 3 {
		t.Fatalf("job after migrate: %+v", j)
	}

	n, err := CountReceipts(s, 7)
	if err != nil || n != 1 {
		t.Fatalf("receipts %d %v", n, err)
	}
	rev, err := LatestReviewJSON(s, 7)
	if err != nil || rev != `{"decision":"keep"}` {
		t.Fatalf("review %q %v", rev, err)
	}
	acts, err := CountIntended(s, "Sannrox/rusui", 42)
	if err != nil || acts != 1 {
		t.Fatalf("actions %d %v", acts, err)
	}

	var sessionID, envID int64
	var kind string
	if err := s.DB.QueryRow(`SELECT id, environment_id, kind FROM sessions WHERE repo=? AND item=?`, "Sannrox/rusui", 42).Scan(&sessionID, &envID, &kind); err != nil {
		t.Fatal(err)
	}
	sess, err := GetSession(s, sessionID)
	if err != nil || sess.GuestName != "" || sess.GuestPin != "" || sess.GuestConfig != "" {
		t.Fatalf("legacy session guest binding: %+v %v", sess, err)
	}
	if sessionID == 0 || envID != DefaultEnvironmentID || kind != SessionKindReview {
		t.Fatalf("session %d env %d kind %s", sessionID, envID, kind)
	}
	var turnSession int64
	if err := s.DB.QueryRow(`SELECT session_id FROM turns WHERE id=7`).Scan(&turnSession); err != nil {
		t.Fatal(err)
	}
	if turnSession != sessionID {
		t.Fatalf("turn session %d want %d", turnSession, sessionID)
	}
	var events int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE delivery_id='del-1' AND session_id=?`, sessionID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("events %d %v", events, err)
	}
	var runners int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM runners WHERE name=?`, LocalRunnerName).Scan(&runners); err != nil || runners != 1 {
		t.Fatalf("runners %d %v", runners, err)
	}
	var handle sql.NullString
	if err := s.DB.QueryRow(`SELECT handle FROM environments WHERE id=?`, DefaultEnvironmentID).Scan(&handle); err != nil {
		t.Fatalf("v4 handle column: %v", err)
	}
	payload, err := LatestPolicyPayload(s)
	if err != nil || string(payload) != "version: 2\nprojects: {}\n" {
		t.Fatalf("policy revision %q %v", payload, err)
	}
	var budget int
	if err := s.DB.QueryRow(`SELECT count FROM daily_review_counts WHERE repo=? AND day=?`, "Sannrox/rusui", "2026-01-01").Scan(&budget); err != nil || budget != 3 {
		t.Fatalf("budget count %d %v", budget, err)
	}
	var lock int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM schema_upgrade_lock`).Scan(&lock); err != nil || lock != 0 {
		t.Fatalf("upgrade lock left behind %d %v", lock, err)
	}
}

func TestV22SteersReceiveFollowUpOrder(t *testing.T) {
	storetest.SQLiteOnly(t, "SQLite migration steps; PostgreSQL starts at the baseline")
	path := filepath.Join(t.TempDir(), "v22.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`DROP TABLE turn_steers;
CREATE TABLE turn_steers (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id INTEGER NOT NULL,
  turn_id INTEGER NOT NULL,
  lease_generation INTEGER NOT NULL,
  prompt TEXT NOT NULL,
  acknowledged INTEGER NOT NULL DEFAULT 0,
  promoted INTEGER NOT NULL DEFAULT 0,
  offered INTEGER NOT NULL DEFAULT 0,
  received INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX turn_steers_pending ON turn_steers(turn_id, lease_generation, acknowledged, promoted, id);
CREATE INDEX turn_steers_unreceived ON turn_steers(turn_id, lease_generation, acknowledged, promoted, received, id);
INSERT INTO followup_queue (session_id, seq, prompt) VALUES (77, 1, 'already queued');
INSERT INTO turn_steers (session_id, turn_id, lease_generation, prompt) VALUES (77, 9, 2, 'pending steer');
DELETE FROM schema_migrations WHERE version>=23;`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	var seq int
	if err := upgraded.DB.QueryRow(`SELECT followup_seq FROM turn_steers WHERE session_id=77`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if seq != 2 {
		t.Fatalf("upgraded steer sequence %d, want 2 after the existing follow-up", seq)
	}
}

func TestLocalRuntimeMigrationsPreserveManagedState(t *testing.T) {
	storetest.SQLiteOnly(t, "SQLite migration steps; PostgreSQL starts at the baseline")
	path := filepath.Join(t.TempDir(), "v16.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`DROP TABLE IF EXISTS process_attaches; DROP TABLE IF EXISTS session_processes;
		DELETE FROM schema_migrations WHERE version>=17`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 23, 18, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := st.DB.Exec(`INSERT INTO sessions (id, environment_id, kind, repo, item, item_kind, state, created_at)
		VALUES (501, 1, 'run', 'example/test-repo', -1, 'run', 'open', ?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO turns (id, session_id, lane, lease_generation, state) VALUES (601, 501, 'run', 4, 'leased')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO receipts (job_id, lease_generation, claimed_revision, kind, payload)
		VALUES (601, 4, 3, 'complete', '{"preserved":true}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO approval_decisions (action_id, decision, decided_at) VALUES ('approval-1', 'allow', ?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO terminal_leases (environment_id, session_id, generation, expires_at)
		VALUES (1, 501, 9, ?)`, now); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	var version int
	if err := upgraded.DB.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != CurrentSchema {
		t.Fatalf("schema version %d: %v", version, err)
	}
	var sessionState, kind, turnState string
	var envID int64
	if err := upgraded.DB.QueryRow(`SELECT kind, state, environment_id FROM sessions WHERE id=501`).Scan(&kind, &sessionState, &envID); err != nil {
		t.Fatal(err)
	}
	if kind != SessionKindRun || sessionState != "open" || envID != DefaultEnvironmentID {
		t.Fatalf("session kind=%s state=%s environment=%d", kind, sessionState, envID)
	}
	var environmentCount int
	if err := upgraded.DB.QueryRow(`SELECT COUNT(*) FROM environments WHERE id=?`, envID).Scan(&environmentCount); err != nil || environmentCount != 1 {
		t.Fatalf("preserved environment count=%d: %v", environmentCount, err)
	}
	var generation int
	if err := upgraded.DB.QueryRow(`SELECT state, lease_generation FROM turns WHERE id=601`).Scan(&turnState, &generation); err != nil {
		t.Fatal(err)
	}
	if turnState != "leased" || generation != 4 {
		t.Fatalf("turn state=%s generation=%d", turnState, generation)
	}
	var receipt, approval, terminal int
	if err := upgraded.DB.QueryRow(`SELECT COUNT(*) FROM receipts WHERE job_id=601 AND lease_generation=4 AND payload='{"preserved":true}'`).Scan(&receipt); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.DB.QueryRow(`SELECT COUNT(*) FROM approval_decisions WHERE action_id='approval-1' AND decision='allow'`).Scan(&approval); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.DB.QueryRow(`SELECT COUNT(*) FROM terminal_leases WHERE environment_id=1 AND session_id=501 AND generation=9`).Scan(&terminal); err != nil {
		t.Fatal(err)
	}
	if receipt != 1 || approval != 1 || terminal != 1 {
		t.Fatalf("preserved receipt=%d approval=%d terminal_lease=%d", receipt, approval, terminal)
	}
	for _, table := range []string{"session_processes", "process_attaches"} {
		var count int
		if err := upgraded.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s count=%d: %v", table, count, err)
		}
	}
	var identityHash sql.NullString
	if err := upgraded.DB.QueryRow(`SELECT identity_hash FROM session_processes LIMIT 1`).Scan(&identityHash); err != sql.ErrNoRows {
		t.Fatalf("identity_hash migration result %v, want empty table with column present", err)
	}
}

// Each database gets one plane identity that survives reopening (#406).
func TestPlaneIDStablePerDatabase(t *testing.T) {
	open := func(dsn string) string {
		t.Helper()
		s, err := openTestDSN(dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		id, err := PlaneID(s)
		if err != nil || len(id) != 8 {
			t.Fatalf("plane id %q %v", id, err)
		}
		return id
	}
	dsnA := storetest.DSN(t)
	a := open(dsnA)
	if again := open(dsnA); again != a {
		t.Fatalf("reopen changed plane id %s -> %s", a, again)
	}
	if b := open(storetest.DSN(t)); b == a {
		t.Fatalf("two databases share plane id %s", a)
	}
}

// A v32 follow-up queue gains the queued and dropped flags (#492); rows
// already queued stay ordinary follow-ups.
func TestV33FollowUpsGainQueuedFlag(t *testing.T) {
	storetest.SQLiteOnly(t, "SQLite migration steps; PostgreSQL starts at the baseline")
	path := filepath.Join(t.TempDir(), "v32.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`DROP TABLE followup_queue;
CREATE TABLE followup_queue (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id INTEGER NOT NULL,
  seq INTEGER NOT NULL,
  prompt TEXT NOT NULL,
  consumed INTEGER NOT NULL DEFAULT 0,
  UNIQUE(session_id, seq)
);
INSERT INTO followup_queue (session_id, seq, prompt) VALUES (5, 1, 'ordinary');
DELETE FROM schema_migrations WHERE version>=33;`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	err = upgraded.Tx(func(tx *sql.Tx) error {
		if _, err := EnqueueQueuedPromptTx(tx, 5, "held"); err != nil {
			return err
		}
		next, ok, err := NextFollowUpTx(tx, 5)
		if err != nil || !ok || next.Prompt != "ordinary" || next.Queued {
			t.Fatalf("head %+v ok %t err %v", next, ok, err)
		}
		n, err := DropQueuedPromptsTx(tx, 5)
		if err != nil || n != 1 {
			t.Fatalf("dropped %d err %v", n, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var dropped int
	if err := upgraded.DB.QueryRow(`SELECT dropped FROM followup_queue WHERE session_id=5 AND seq=2`).Scan(&dropped); err != nil || dropped != 1 {
		t.Fatalf("dropped %d err %v", dropped, err)
	}
}

func TestMigrateV43MovesBodyToDisk(t *testing.T) {
	storetest.SQLiteOnly(t, "SQLite migration steps; PostgreSQL starts at the baseline")
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	body := []byte{0x89, 0x50, 0x4e, 0x47}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.DB.Exec(`INSERT INTO prompt_attachments (session_id, seq, revision, name, digest, mime, body, path, created_at) VALUES (1,1,1,'shot.png',?,'image/png',?,'',?)`, digest, body, now); err != nil {
		t.Fatal(err)
	}
	if err := migrateV43(s); err != nil {
		t.Fatal(err)
	}
	var bodyLen int
	var path string
	if err := s.DB.QueryRow(`SELECT length(body), path FROM prompt_attachments WHERE digest=?`, digest).Scan(&bodyLen, &path); err != nil {
		t.Fatal(err)
	}
	if bodyLen != 0 || path != digest {
		t.Fatalf("sqlite body=%d path=%q", bodyLen, path)
	}
	raw, err := os.ReadFile(s.AttachmentFile(digest))
	if err != nil || !bytes.Equal(raw, body) {
		t.Fatalf("disk %v %v", raw, err)
	}
}

func TestMigrateV49BackfillsImplementWriteGrant(t *testing.T) {
	storetest.SQLiteOnly(t, "SQLite migration steps; PostgreSQL starts at the baseline")
	path := filepath.Join(t.TempDir(), "v49.db")
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
CREATE TABLE sessions (id INTEGER PRIMARY KEY, execution_mode TEXT NOT NULL DEFAULT 'space');
CREATE TABLE tasks (id INTEGER PRIMARY KEY, session_id INTEGER NOT NULL, state TEXT NOT NULL);
INSERT INTO sessions (id, execution_mode) VALUES (1, 'space'), (2, 'space'), (3, 'user');
INSERT INTO tasks (session_id, state) VALUES (1, 'open'), (2, 'abandoned'), (3, 'open');
`); err != nil {
		t.Fatal(err)
	}
	if err := migrateV49(db); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_tool_grants WHERE session_id=1 AND tool=? AND revoked_at IS NULL`, ToolWrite).Scan(&n); err != nil || n != 1 {
		t.Fatalf("open task backfill %d %v", n, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_tool_grants WHERE session_id=2`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("abandoned task granted %d %v", n, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_tool_grants WHERE session_id=3`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("user-mode task granted %d %v", n, err)
	}
	if err := migrateV49(db); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_tool_grants WHERE session_id=1 AND tool=? AND revoked_at IS NULL`, ToolWrite).Scan(&n); err != nil || n != 1 {
		t.Fatalf("retry backfill %d %v", n, err)
	}
}

// The next release's expand-only migration (here a column this release does
// not know) keeps the floor, so this release serves the migrated store beside
// the next one during a rolling upgrade, and alone after a rollback, without
// changing its schema (ADR 0089 D8).
func TestPreviousReleaseServesAnExpandedSchema(t *testing.T) {
	dsn := storetest.DSN(t)
	next := openTestStoreAt(t, dsn)
	if _, err := next.DB.Exec(`ALTER TABLE sessions ADD COLUMN next_release_note TEXT`); err != nil {
		t.Fatal(err)
	}
	if err := StampSchemaVersion(next, CurrentSchema+1, ReadableFloor); err != nil {
		t.Fatal(err)
	}
	prev := openTestStoreAt(t, dsn)
	var sid int64
	if err := prev.Tx(func(tx *sql.Tx) error {
		var err error
		sid, _, err = InsertRunSessionTx(tx, "test", "example/test-repo", "written by the previous release")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if sess, err := GetSession(next, sid); err != nil || sess.Prompt != "written by the previous release" {
		t.Fatalf("session %+v %v", sess, err)
	}
	var floor int
	if err := prev.DB.QueryRow(`SELECT floor FROM schema_floor`).Scan(&floor); err != nil || floor != ReadableFloor {
		t.Fatalf("floor %d %v", floor, err)
	}
	if ver, err := SchemaVersion(prev); err != nil || ver != CurrentSchema+1 {
		t.Fatalf("schema %d %v", ver, err)
	}
}

// A contract migration raises the floor past this release, and a newer store
// without a floor predates it; this release refuses both, as before.
func TestNewerSchemaIsRejected(t *testing.T) {
	for name, floor := range map[string]int{"contract step": CurrentSchema + 1, "no floor": 0} {
		t.Run(name, func(t *testing.T) {
			dsn := storetest.DSN(t)
			st := openTestStoreAt(t, dsn)
			if err := StampSchemaVersion(st, CurrentSchema+1, floor); err != nil {
				t.Fatal(err)
			}
			if floor == 0 {
				if _, err := st.DB.Exec(`DELETE FROM schema_floor`); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			_, err := openTestDSN(dsn)
			if err == nil || !strings.Contains(err.Error(), "newer than this binary") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

// A newer release that is still migrating, or died doing so, leaves a schema
// no release can vouch for; this release refuses it even under its floor.
func TestNewerSchemaMidUpgradeIsRefused(t *testing.T) {
	storetest.SQLiteOnly(t, "SQLite upgrade lock; PostgreSQL migrates in one transaction")
	path := filepath.Join(t.TempDir(), "mid.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := StampSchemaVersion(st, CurrentSchema+1, ReadableFloor); err != nil {
		t.Fatal(err)
	}
	if err := setUpgradeLock(st.DB, CurrentSchema+2); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "in progress or was interrupted") {
		t.Fatalf("got %v", err)
	}
}

func TestInterruptedUpgradeFailsClosed(t *testing.T) {
	storetest.SQLiteOnly(t, "SQLite upgrade lock; the PostgreSQL baseline applies in one transaction")
	path := filepath.Join(t.TempDir(), "mid.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`DELETE FROM schema_migrations WHERE version=?`, CurrentSchema); err != nil {
		t.Fatal(err)
	}
	if err := setUpgradeLock(st.DB, CurrentSchema); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = Open(path)
	if err == nil || !strings.Contains(err.Error(), "interrupted upgrade") {
		t.Fatalf("got %v", err)
	}
}

func TestCompletedUpgradeClearsStaleLock(t *testing.T) {
	storetest.SQLiteOnly(t, "SQLite upgrade lock; the PostgreSQL baseline applies in one transaction")
	path := filepath.Join(t.TempDir(), "done.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := setUpgradeLock(st.DB, CurrentSchema); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var n int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM schema_upgrade_lock`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("lock %d %v", n, err)
	}
	ver, err := SchemaVersion(st)
	if err != nil || ver != CurrentSchema {
		t.Fatalf("schema %d %v", ver, err)
	}
}

// V55 (#717) adds nullable attribution columns: a decision written by the
// previous release reads back without an actor, and a migration interrupted
// after some columns were added completes on the next open.
func TestV55DecisionsGainNullableAttribution(t *testing.T) {
	storetest.SQLiteOnly(t, "SQLite migration steps; PostgreSQL starts at the baseline")
	path := filepath.Join(t.TempDir(), "v54.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`
ALTER TABLE approval_decisions DROP COLUMN decided_by;
ALTER TABLE approval_decisions DROP COLUMN delegated_by;
ALTER TABLE result_dispositions DROP COLUMN delegated_by;
DELETE FROM schema_migrations WHERE version>=55;
INSERT INTO approval_decisions(action_id, decision, decided_at) VALUES ('old', 'allow', '2026-11-01T00:00:00Z');
`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var by, via sql.NullString
	if err := st.DB.QueryRow(`SELECT decided_by, delegated_by FROM approval_decisions WHERE action_id='old'`).Scan(&by, &via); err != nil {
		t.Fatal(err)
	}
	if by.Valid || via.Valid {
		t.Fatalf("old row gained an actor: %v %v", by, via)
	}
	if err := PutApprovalDecision(st, "new", "deny", Attribution{Actor: "alice", Delegate: "svc"}); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRow(`SELECT decided_by, delegated_by FROM approval_decisions WHERE action_id='new'`).Scan(&by, &via); err != nil || by.String != "alice" || via.String != "svc" {
		t.Fatalf("new row %v %v %v", by, via, err)
	}
}

// A schema 56 store gains the nullable policy binding on upgrade; older
// reviews read back with no revision (#661).
func TestV57ReviewsGainNullablePolicyBinding(t *testing.T) {
	storetest.SQLiteOnly(t, "SQLite migration steps; PostgreSQL starts at the baseline")
	path := filepath.Join(t.TempDir(), "v56.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`
ALTER TABLE turns DROP COLUMN policy_revision_id;
ALTER TABLE review_revisions DROP COLUMN policy_revision_id;
DELETE FROM schema_migrations WHERE version>=57;
`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for _, table := range []string{"turns", "review_revisions"} {
		var n int
		if err := st.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name='policy_revision_id'`, table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s policy_revision_id: %d %v", table, n, err)
		}
	}
}

package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestV1DatabaseUpgradesInPlace(t *testing.T) {
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
	var pubs int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='publication_attempts'`).Scan(&pubs); err != nil || pubs != 1 {
		t.Fatalf("publication_attempts missing after upgrade: %d %v", pubs, err)
	}
	var fb int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='effort_feedback'`).Scan(&fb); err != nil || fb != 1 {
		t.Fatalf("effort_feedback missing after upgrade: %d %v", fb, err)
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
}

func TestV17AddsProcessAndAttachTablesWithoutRewritingRuntimeState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v16.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`DROP TABLE IF EXISTS process_attaches; DROP TABLE IF EXISTS session_processes;
		DELETE FROM schema_migrations WHERE version=?`, CurrentSchema); err != nil {
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
}

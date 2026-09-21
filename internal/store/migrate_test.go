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

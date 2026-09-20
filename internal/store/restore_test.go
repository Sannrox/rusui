package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRestoreMissingAndCorruptArtifacts(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := Restore(filepath.Join(dir, "missing.db")); err == nil {
		t.Fatal("missing artifact succeeded")
	}
	bad := filepath.Join(dir, "bad.db")
	if err := os.WriteFile(bad, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Restore(bad); err == nil {
		t.Fatal("corrupt artifact succeeded")
	}
}

func TestRestoreClearsStaleAuthority(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "rusui.db")
	st, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	var sid, tid int64
	err = st.Tx(func(tx *sql.Tx) error {
		id, _, err := InsertRunSessionTx(tx, "test", "example/test-repo", "hello")
		if err != nil {
			return err
		}
		sid = id
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRow(`SELECT id FROM turns WHERE session_id=?`, sid).Scan(&tid); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	if _, err := st.DB.Exec(`UPDATE turns SET state='leased', lease_generation=3, lease_expires_at=? WHERE id=?`, now, tid); err != nil {
		t.Fatal(err)
	}
	if err := PutTurnCredential(st, tid, 3, "deadbeef", now); err != nil {
		t.Fatal(err)
	}
	if err := PutApprovalDecision(st, "old-allow", "allow"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "restore.db")
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		t.Fatal(err)
	}
	st2, rep, err := Restore(dst)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	if rep.Sessions < 1 || rep.LeasesCleared != 1 || rep.GrantsDropped != 1 || rep.ApprovalRecords != 1 {
		t.Fatalf("report %+v", rep)
	}
	sess, err := GetSession(st2, sid)
	if err != nil || sess.Prompt != "hello" {
		t.Fatalf("session %+v %v", sess, err)
	}
	var state string
	if err := st2.DB.QueryRow(`SELECT state FROM turns WHERE id=?`, tid).Scan(&state); err != nil || state != "queued" {
		t.Fatalf("turn state %s %v", state, err)
	}
	_, _, ok, err := TurnTokenSession(st2, "deadbeef", time.Now().UTC())
	if err != nil || ok {
		t.Fatalf("stale grant still valid ok=%v %v", ok, err)
	}
	dec, found, err := GetApprovalDecision(st2, "old-allow")
	if err != nil || !found || dec != "allow" {
		t.Fatalf("approval record %s found=%v %v", dec, found, err)
	}
}

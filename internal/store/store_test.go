package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Two handles on one file, as when local attach writes beside the server.
// A read-then-write transaction must wait for a concurrent writer instead of
// failing with SQLITE_BUSY on lock upgrade.
func TestTxWaitsForConcurrentWriterAcrossHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locks.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	if _, err := a.DB.Exec(`CREATE TABLE lock_probe (x INTEGER)`); err != nil {
		t.Fatal(err)
	}

	read := make(chan struct{})
	release := make(chan struct{})
	readerDone := make(chan error, 1)
	go func() {
		readerDone <- b.Tx(func(tx *sql.Tx) error {
			var n int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM lock_probe`).Scan(&n); err != nil {
				return err
			}
			close(read)
			<-release
			_, err := tx.Exec(`INSERT INTO lock_probe (x) VALUES (1)`)
			return err
		})
	}()
	<-read
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- a.Tx(func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO lock_probe (x) VALUES (2)`)
			return err
		})
	}()
	time.Sleep(100 * time.Millisecond) // let the writer reach its lock wait
	close(release)

	if err := <-readerDone; err != nil {
		t.Fatalf("read-then-write tx: %v", err)
	}
	if err := <-writerDone; err != nil {
		t.Fatalf("concurrent writer tx: %v", err)
	}
	var n int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM lock_probe`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("rows = %d, %v; want 2", n, err)
	}
}

func TestListTranscriptAfterPagesInAppendOrder(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var sid, other int64
	if err := s.Tx(func(tx *sql.Tx) error {
		if sid, err = InsertLocalSessionTx(tx, "test", time.Now().UTC()); err != nil {
			return err
		}
		other, err = InsertLocalSessionTx(tx, "test", time.Now().UTC())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	add := func(session int64, id, typ string) {
		t.Helper()
		if err := InsertAction(s, Action{ID: id, SessionID: &session, Repo: "o/r", Item: 1, Type: typ, ReasonCode: "recorded", EvidenceClass: "plane_observed", Body: id}); err != nil {
			t.Fatal(err)
		}
	}
	add(sid, "zz", "acp.update")
	add(other, "mm", "acp.approval")
	add(sid, "aa", "acp.approval")
	add(sid, "kk", "acp.update")

	first, err := ListTranscriptAfter(s, sid, 0, 2)
	if err != nil || len(first) != 2 || first[0].ID != "zz" || first[1].ID != "aa" {
		t.Fatalf("first page %+v %v", first, err)
	}
	rest, err := ListTranscriptAfter(s, sid, first[1].Seq, 2)
	if err != nil || len(rest) != 1 || rest[0].ID != "kk" || rest[0].Seq <= first[1].Seq {
		t.Fatalf("next page %+v %v", rest, err)
	}
	var plan string
	if err := s.DB.QueryRow(`EXPLAIN QUERY PLAN SELECT rowid FROM actions WHERE session_id=? AND rowid>? ORDER BY rowid LIMIT 1`, sid, 0).Scan(new(int), new(int), new(int), &plan); err != nil || !strings.Contains(plan, "actions_session ") {
		t.Fatalf("plan %q %v", plan, err)
	}
}

func TestCountPendingApprovalsOnlyCountsAnswerableRequests(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var sid, tid int64
	if err := s.Tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1, 'run', 'o/r', 1, 'issue', 'open', ?)`, time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		if sid, err = res.LastInsertId(); err != nil {
			return err
		}
		res, err = tx.Exec(`INSERT INTO turns (session_id, lane, state) VALUES (?, 'run', 'leased')`, sid)
		if err != nil {
			return err
		}
		tid, err = res.LastInsertId()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	add := func(id, reason string) {
		t.Helper()
		if err := InsertAction(s, Action{ID: id, SessionID: &sid, TurnID: &tid, Repo: "o/r", Item: 1, Type: "acp.approval", ReasonCode: reason, EvidenceClass: "plane_observed", Body: id}); err != nil {
			t.Fatal(err)
		}
	}
	count := func() int {
		t.Helper()
		n, err := CountPendingApprovals(s, sid)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	add("denied", "denied")
	add("ask", "permission_unmatched")
	if n := count(); n != 1 {
		t.Fatalf("pending %d", n)
	}
	if err := PutApprovalDecision(s, "ask", "allow"); err != nil {
		t.Fatal(err)
	}
	add("again", "permission_unmatched")
	if n := count(); n != 1 {
		t.Fatalf("after decision %d", n)
	}
	// The turn ended while the request waited; nobody can answer it now.
	if _, err := s.DB.Exec(`UPDATE turns SET state='completed' WHERE id=?`, tid); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Fatalf("ended turn %d", n)
	}
}

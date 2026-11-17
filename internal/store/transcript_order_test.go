package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/store/storetest"
)

// A transcript reader that resumes after the highest Seq it has seen never
// misses an entry, even when the writer that inserted first commits last
// (#744). PostgreSQL only: SQLite has one writer, and its single test
// connection cannot read while another transaction is open.
func TestTranscriptResumeSeesEntriesCommittedOutOfInsertOrder(t *testing.T) {
	storetest.PostgresOnly(t)
	st := openTestStore(t)
	var sid int64
	if err := st.DB.QueryRow(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1,'run','o/r',1,'issue','open','2026-11-17T00:00:00Z') RETURNING id`).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	action := func(id string) Action {
		return Action{ID: id, SessionID: &sid, Repo: "o/r", Item: 1, Type: "acp.session.update", ReasonCode: "recorded", EvidenceClass: "plane_observed", LimitSentence: "limit", Body: "{}"}
	}
	first, err := st.DB.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Rollback() }()
	if err := InsertActionTx(first, action("first")); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- st.Tx(func(tx *sql.Tx) error { return InsertActionTx(tx, action("second")) }) }()
	// Give the second writer the chance to commit ahead of the first.
	select {
	case err := <-done:
		done <- err
	case <-time.After(300 * time.Millisecond):
	}
	seen, err := ListTranscriptAfter(st, sid, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var after int64
	for _, e := range seen {
		after = max(after, e.Seq)
	}
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	rest, err := ListTranscriptAfter(st, sid, after, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(seen) + len(rest); got != 2 {
		t.Fatalf("reader saw %d then %d entries after Seq %d; want 2 in total", len(seen), len(rest), after)
	}
}

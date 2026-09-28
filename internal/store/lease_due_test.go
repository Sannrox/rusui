package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestListDueLeasedJobsTxSkipsLiveDeadlines(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "due.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	live := now.Add(time.Hour)
	due := now.Add(-time.Minute)
	var dueID int64
	if err := st.Tx(func(tx *sql.Tx) error {
		j1 := &Job{Repo: "o/r", Item: 1, ItemKind: "issue", Lane: "review", State: "leased", LeaseExpiresAt: &live, ExecutionDeadlineAt: &live}
		if err := InsertJobTx(tx, j1); err != nil {
			return err
		}
		if err := UpdateJobTx(tx, j1); err != nil {
			return err
		}
		j2 := &Job{Repo: "o/r", Item: 2, ItemKind: "issue", Lane: "review", State: "leased", LeaseExpiresAt: &due, ExecutionDeadlineAt: &due}
		if err := InsertJobTx(tx, j2); err != nil {
			return err
		}
		if err := UpdateJobTx(tx, j2); err != nil {
			return err
		}
		dueID = j2.ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var got []*Job
	if err := st.Tx(func(tx *sql.Tx) error {
		var err error
		got, err = ListDueLeasedJobsTx(tx, now, "", "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != dueID {
		t.Fatalf("due jobs %+v want id %d", got, dueID)
	}
}

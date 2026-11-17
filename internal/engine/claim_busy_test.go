package engine

import (
	"database/sql"
	"testing"

	"github.com/sannrox/rusui/internal/store"
)

func TestRequeueBusyReviewClaimRestoresDailyBudget(t *testing.T) {
	st := OpenTestStore(t)

	job := &store.Job{
		Repo: "example/test-repo", Item: 1, ItemKind: "issue", Lane: "review",
		PendingRevision: 1, ClaimedRevision: 1, LeaseGeneration: 1, State: "leased",
	}
	const reviewDay = "2026-09-10"
	if err := st.Tx(func(tx *sql.Tx) error {
		if err := store.InsertJobTx(tx, job); err != nil {
			return err
		}
		return store.IncrReviewsToday(tx, job.Repo, reviewDay)
	}); err != nil {
		t.Fatal(err)
	}

	e := &Engine{Store: st}
	if err := e.requeueBusyEnvironmentClaim(&Claim{Job: job, reviewCountDay: reviewDay}); err != nil {
		t.Fatal(err)
	}

	turn, err := store.GetTurn(st, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turn.State != "queued" || turn.RetryCount != 0 {
		t.Fatalf("requeued turn = state %s, retry count %d; want queued with no retry", turn.State, turn.RetryCount)
	}
	var count int
	if err := st.Tx(func(tx *sql.Tx) error {
		var err error
		count, err = store.CountReviewsToday(tx, job.Repo, reviewDay)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("daily review count = %d, want 0 after requeue", count)
	}
}

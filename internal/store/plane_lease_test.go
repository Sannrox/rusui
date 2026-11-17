package store

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

// The lease is fenced by the store clock on both backends: a live holder
// renews at its generation, others are refused, and a takeover after expiry
// bumps the generation so the old holder's Check fails.
func TestPlaneLeaseFencesOnStoreClock(t *testing.T) {
	st := openTestStore(t)
	a, ok, err := ClaimPlaneLease(st, SchedulerLease, "a", time.Minute)
	if err != nil || !ok || a.Generation != 1 {
		t.Fatalf("claim a %+v ok=%v %v", a, ok, err)
	}
	if _, ok, err := ClaimPlaneLease(st, SchedulerLease, "b", time.Minute); err != nil || ok {
		t.Fatalf("b claimed a live lease ok=%v %v", ok, err)
	}
	if renewed, ok, err := ClaimPlaneLease(st, SchedulerLease, "a", time.Minute); err != nil || !ok || renewed.Generation != a.Generation {
		t.Fatalf("renew %+v ok=%v %v", renewed, ok, err)
	}
	if _, err := st.DB.Exec(`UPDATE plane_job_leases SET expires_at = unixepoch() - 1 WHERE name = ?`, SchedulerLease); err != nil {
		t.Fatal(err)
	}
	b, ok, err := ClaimPlaneLease(st, SchedulerLease, "b", time.Minute)
	if err != nil || !ok || b.Generation != a.Generation+1 {
		t.Fatalf("takeover %+v ok=%v %v", b, ok, err)
	}
	if err := st.Tx(func(tx *sql.Tx) error { return a.Check(tx) }); !errors.Is(err, ErrPlaneLeaseLost) {
		t.Fatalf("old holder check %v", err)
	}
	if err := st.Tx(func(tx *sql.Tx) error { return b.Check(tx) }); err != nil {
		t.Fatalf("new holder check %v", err)
	}
}

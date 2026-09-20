package engine_test

import (
	"testing"

	"github.com/sannrox/rusui/internal/store"
)

func TestDrainStopsNewClaimsAndListsLive(t *testing.T) {
	h := setup(t)
	h.putRefresh(issue(1))
	c := h.claim()
	rep, err := h.e.Drain()
	if err != nil || !rep.Paused {
		t.Fatalf("drain %+v %v", rep, err)
	}
	if len(rep.Live) != 1 || rep.Live[0].TurnID != c.Job.ID {
		t.Fatalf("live %+v", rep.Live)
	}
	c2, err := h.e.Claim("example/test-repo")
	if c2 != nil || err == nil {
		t.Fatalf("claimed after drain c=%v err=%v", c2, err)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err != nil {
		t.Fatal(err)
	}
}

func TestDrainPreservesReceipts(t *testing.T) {
	h := setup(t)
	h.putRefresh(issue(1))
	c := h.claim()
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err != nil {
		t.Fatal(err)
	}
	n, err := store.CountReceipts(h.st, c.Job.ID)
	if err != nil || n < 1 {
		t.Fatalf("receipts %d %v", n, err)
	}
	if _, err := h.e.Drain(); err != nil {
		t.Fatal(err)
	}
	n2, err := store.CountReceipts(h.st, c.Job.ID)
	if err != nil || n2 != n {
		t.Fatalf("receipts after drain %d want %d %v", n2, n, err)
	}
}

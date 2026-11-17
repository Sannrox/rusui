package engine_test

import (
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/receipt"
	"github.com/sannrox/rusui/internal/store"
)

// A review's receipt cites the policy its turn was claimed under, not the
// plane's current policy (#661).
func TestReceiptCitesPolicyTheReviewWasClaimedUnder(t *testing.T) {
	h := setup(t)
	claimedUnder := h.e.PolicySnapshot().Hash
	h.putRefresh(issue(1))
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatalf("claim %v %v", c, err)
	}
	later, err := policy.Parse([]byte(strings.Replace(fixture, "max_reviews_per_repo_per_utc_day: 50", "max_reviews_per_repo_per_utc_day: 40", 1)))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(later)
	if later.Hash == claimedUnder {
		t.Fatal("reloaded policy must differ")
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err != nil {
		t.Fatal(err)
	}
	turn, err := store.GetTurn(h.st, c.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	exp, err := receipt.FromStore(h.st, turn.SessionID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if exp.PolicyRevision != claimedUnder || exp.PolicyRevisionID == 0 {
		t.Fatalf("receipt cites %q (id %d), want %q", exp.PolicyRevision, exp.PolicyRevisionID, claimedUnder)
	}
}

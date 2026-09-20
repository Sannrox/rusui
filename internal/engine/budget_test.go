package engine_test

import (
	"errors"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/store"
)

func TestClaimRespectsConcurrentLeaseCap(t *testing.T) {
	h := setup(t)
	h.putRefresh(issue(1))
	h.putRefresh(issue(2))
	c1 := h.claim()
	c2, err := h.e.Claim("example/test-repo")
	if !errors.Is(err, engine.ErrBudget) || c2 != nil {
		t.Fatalf("second claim %v %v", c2, err)
	}
	turn, err := store.GetTurn(h.st, c1.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.e.CancelSession(turn.SessionID); err != nil {
		t.Fatal(err)
	}
	c3, err := h.e.Claim("example/test-repo")
	if err != nil || c3 == nil {
		t.Fatalf("claim after cancel %v %v", c3, err)
	}
	if c3.Job.Item == c1.Job.Item {
		t.Fatalf("expected the other item, got %d", c3.Job.Item)
	}
}

package engine_test

import (
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

func TestStepSchedulesBucketsAndSkipLive(t *testing.T) {
	h := setup(t)
	id, err := h.e.CreateSchedule("test", "hourly", "1m", "scheduled work")
	if err != nil || id == 0 {
		t.Fatalf("%d %v", id, err)
	}
	t0 := time.Unix(1_700_000_000, 0).UTC()
	if err := h.e.StepSchedules(t0); err != nil {
		t.Fatal(err)
	}
	if err := h.e.StepSchedules(t0.Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	n := countKind(t, h.st, store.SessionKindScheduled)
	if n != 1 {
		t.Fatalf("same bucket %d", n)
	}
	if err := h.e.StepSchedules(t0.Add(61 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if countKind(t, h.st, store.SessionKindScheduled) != 1 {
		t.Fatal("live session should skip")
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil || c.Job.Lane != "scheduled" {
		t.Fatalf("claim %+v %v", c, err)
	}
}

func TestCreateScheduleRequiresKind(t *testing.T) {
	h := setup(t)
	if _, err := h.e.CreateSchedule("missing", "n", "1m", "p"); err == nil {
		t.Fatal("expected policy error")
	}
}

func countKind(t *testing.T, st *store.Store, kind string) int {
	t.Helper()
	var n int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE kind=?`, kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

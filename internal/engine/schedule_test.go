package engine_test

import (
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/policy"
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

func TestScheduleFireAfterCompleteMintsNewSession(t *testing.T) {
	h := setup(t)
	id, err := h.e.CreateSchedule("test", "hourly", "1m", "scheduled work")
	if err != nil || id == 0 {
		t.Fatalf("%d %v", id, err)
	}
	t0 := time.Unix(1_700_000_000, 0).UTC()
	if err := h.e.StepSchedules(t0); err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	turn, err := store.GetTurn(h.st, c.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	first := turn.SessionID
	if err := h.e.CancelSession(first); err != nil {
		t.Fatal(err)
	}
	if err := h.e.StepSchedules(t0.Add(61 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if n := countKind(t, h.st, store.SessionKindScheduled); n != 2 {
		t.Fatalf("after complete fire %d sessions", n)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil {
		t.Fatalf("second fire claim %+v %v", c2, err)
	}
	turn2, err := store.GetTurn(h.st, c2.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turn2.SessionID == first {
		t.Fatal("fire continued the old session")
	}
}

const twoProjectFixture = `version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
  review: true
  comments: true
  close: true
  implement: false
  land: false
  max_reviews_per_repo_per_utc_day: 50
projects:
  test:
    repos:
      example/test-repo:
        visibility: public
        review: true
        comments: true
        close: true
        implement: false
        land: false
  test2:
    repos:
      example/test-repo-2:
        visibility: public
        review: true
        comments: true
        close: true
        implement: false
        land: false
`

// A schedule whose project StartScheduled refuses must not block schedules
// listed after it from starting, and the refusal must still be reported.
func TestStepSchedulesContinuesPastRefusalAndReportsIt(t *testing.T) {
	h := setup(t)
	twoProjects, err := policy.Parse([]byte(twoProjectFixture))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(twoProjects)

	badID, err := h.e.CreateSchedule("test", "will-be-refused", "1m", "scheduled work")
	if err != nil || badID == 0 {
		t.Fatalf("%d %v", badID, err)
	}
	goodID, err := h.e.CreateSchedule("test2", "stays-healthy", "1m", "scheduled work")
	if err != nil || goodID == 0 {
		t.Fatalf("%d %v", goodID, err)
	}

	// Drop "test" from policy after the schedule exists, so StartScheduled
	// refuses it on the next step while "test2" remains healthy.
	onlyTest2, err := policy.Parse([]byte(`version: 2
defaults:
  session_kinds: [review, run, scheduled]
  egress: trusted
projects:
  test2:
    repos:
      example/test-repo-2:
        visibility: public
`))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(onlyTest2)

	t0 := time.Unix(1_700_000_000, 0).UTC()
	if err := h.e.StepSchedules(t0); err == nil {
		t.Fatal("expected refusal to be reported")
	}
	if n := countKind(t, h.st, store.SessionKindScheduled); n != 1 {
		t.Fatalf("healthy schedule after the refused one should still start, got %d", n)
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

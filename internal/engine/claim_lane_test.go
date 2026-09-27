package engine_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

const lanePolicy = `version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
  review: false
  comments: false
  close: false
  implement: true
  land: false
  max_reviews_per_repo_per_utc_day: 50
projects:
  test:
    budgets:
      max_concurrent_leases: 3
    repos:
      example/test-repo:
        visibility: public
        review: false
`

func laneHarness(t *testing.T, pol string) *harn {
	t.Helper()
	h := setup(t)
	p, err := policy.Parse([]byte(pol))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(p)
	return h
}

func TestClaimPrefersLaterRunOverQueuedReviews(t *testing.T) {
	h := setup(t)
	const n = 8
	for i := 1; i <= n; i++ {
		h.putRefresh(issue(i))
	}
	const prompt = "implement the pinned task"
	if _, err := h.e.StartRun("test", prompt, ""); err != nil {
		t.Fatal(err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatalf("claim %v %v", c, err)
	}
	if c.Job.Lane != "run" || c.Snapshot.Body != prompt {
		t.Fatalf("claimed lane %s item %d body %q", c.Job.Lane, c.Job.Item, c.Snapshot.Body)
	}
	for i := 1; i <= n; i++ {
		j, err := store.JobState(h.st, "example/test-repo", i)
		if err != nil || j == nil || j.Lane != "review" || j.State != "queued" {
			t.Fatalf("review %d: %+v %v", i, j, err)
		}
	}
}

func TestRunClaimDoesNotNeedReviewPolicy(t *testing.T) {
	h := laneHarness(t, lanePolicy)
	if _, err := h.e.StartRun("test", "implement something", ""); err != nil {
		t.Fatal(err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil || c.Job.Lane != "run" {
		t.Fatalf("claim %+v %v", c, err)
	}
}

func TestReviewBudgetDoesNotBlockOrCountRuns(t *testing.T) {
	pol := strings.Replace(strings.Replace(lanePolicy, "        review: false", "        review: true", 1), "max_reviews_per_repo_per_utc_day: 50", "max_reviews_per_repo_per_utc_day: 1", 1)
	h := laneHarness(t, pol)
	var notes []string
	h.e.Notify = func(m string) { notes = append(notes, m) }
	for i := range 2 {
		if _, err := h.e.StartRun("test", "run "+string(rune('a'+i)), ""); err != nil {
			t.Fatal(err)
		}
		c, err := h.e.Claim("example/test-repo")
		if err != nil || c == nil || c.Job.Lane != "run" {
			t.Fatalf("run %d claim %+v %v (runs must not use the review budget)", i, c, err)
		}
	}
	// Spend the one review, then a run claim must still raise the signal.
	if _, err := h.e.Store.DB.Exec(`INSERT INTO daily_review_counts (repo, day, count) VALUES ('example/test-repo', '2026-09-10', 1)
		ON CONFLICT(repo, day) DO UPDATE SET count=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.StartRun("test", "run c", ""); err != nil {
		t.Fatal(err)
	}
	if c, err := h.e.Claim("example/test-repo"); err != nil || c == nil || c.Job.Lane != "run" {
		t.Fatalf("run after budget out: %+v %v", c, err)
	}
	if len(notes) == 0 || !strings.Contains(notes[len(notes)-1], "daily review budget exhausted") {
		t.Fatalf("budget signal not raised: %v", notes)
	}
}

func TestRunWorkWaitsWhenProjectStopsAdmittingRun(t *testing.T) {
	h := laneHarness(t, lanePolicy)
	if _, err := h.e.StartRun("test", "implement something", ""); err != nil {
		t.Fatal(err)
	}
	onlyReview := strings.Replace(lanePolicy, "session_kinds: [review, run, scheduled]", "session_kinds: [review]", 1)
	p, err := policy.Parse([]byte(onlyReview))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(p)
	if c, err := h.e.Claim("example/test-repo"); c != nil || !errors.Is(err, errPolicyFor(t)) {
		t.Fatalf("claim %+v %v", c, err)
	}
}

// errPolicyFor returns the engine's policy refusal by provoking it on an
// unbound repository.
func errPolicyFor(t *testing.T) error {
	t.Helper()
	h := laneHarness(t, lanePolicy)
	_, err := h.e.Claim("other/unbound")
	if err == nil {
		t.Fatal("unbound repo claimable")
	}
	return err
}

var _ = engine.ErrBudget

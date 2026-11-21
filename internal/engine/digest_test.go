package engine_test

import (
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

const digestStopPolicy = `version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
  review: true
  comments: true
  comment: true
  advisory_digest: true
  close: true
  implement: true
  land: false
  repair: docs
  max_reviews_per_repo_per_utc_day: 50
  max_repairs_per_repo_per_utc_day: 1
projects:
  test:
    repos:
      example/test-repo:
        visibility: public
        review: true
        comments: true
        comment: true
        advisory_digest: true
        close: true
        implement: true
        repair: docs
`

func seedActionableDay(t *testing.T, h *harn, day time.Time, startItem, n int) {
	t.Helper()
	created := day.UTC().Format(time.RFC3339Nano)
	for i := range n {
		item := startItem + i
		var sid, tid int64
		if err := h.st.DB.QueryRow(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1, 'review', 'example/test-repo', ?, 'issue', 'open', ?) RETURNING id`, item, created).Scan(&sid); err != nil {
			t.Fatal(err)
		}
		if err := h.st.DB.QueryRow(`INSERT INTO turns (session_id, lane, state) VALUES (?, 'review', 'completed') RETURNING id`, sid).Scan(&tid); err != nil {
			t.Fatal(err)
		}
		if _, err := h.st.DB.Exec(`INSERT INTO review_revisions (job_id, claimed_revision, item_hash, main_sha, payload, created_at) VALUES (?, 1, 'h', 'm', '{"verdict":"keep"}', ?)`, tid, created); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAdvisoryDigestStopsCommentAndRepairAfterThreeDays(t *testing.T) {
	h := setup(t)
	now := h.clk.T
	seedActionableDay(t, h, now.AddDate(0, 0, -2), 100, 11)
	seedActionableDay(t, h, now.AddDate(0, 0, -1), 200, 11)
	seedActionableDay(t, h, now, 300, 11)
	pol, err := policy.Parse([]byte(digestStopPolicy))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(pol)
	h.e.Comments = h.f

	completeComment(t, h, 1)
	if n := len(h.f.IssueComments("example/test-repo", 1)); n != 0 {
		t.Fatalf("live comment posted %d", n)
	}

	h.putRefresh(repairIssue(400))
	if d, r := latestRepair(t, h, 400); d != store.RepairDeclined || r != "repair_off" {
		t.Fatalf("repair %s %s", d, r)
	}

	h.putRefresh(issue(401))
	c := h.claim()
	if c.Job.Lane != "review" {
		t.Fatalf("advisory %+v", c.Job)
	}
}

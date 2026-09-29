package engine_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

func TestAdvisoryEligible(t *testing.T) {
	open := issue(1)
	if !engine.AdvisoryEligible(open) {
		t.Fatal("open issue")
	}
	open.State = "closed"
	if engine.AdvisoryEligible(open) {
		t.Fatal("closed issue")
	}
	pr := issue(2)
	pr.ItemKind = "pull"
	pr.DefaultBranch = "main"
	pr.BaseRef = "main"
	if !engine.AdvisoryEligible(pr) {
		t.Fatal("default-branch PR")
	}
	pr.Draft = true
	if engine.AdvisoryEligible(pr) {
		t.Fatal("draft PR")
	}
	pr.Draft = false
	pr.State = "closed"
	pr.Merged = true
	if engine.AdvisoryEligible(pr) {
		t.Fatal("closed merged default-branch PR")
	}
	pr.Merged = false
	if engine.AdvisoryEligible(pr) {
		t.Fatal("closed default-branch PR")
	}
	pr.State = "open"
	pr.BaseRef = "release-1"
	if engine.AdvisoryEligible(pr) {
		t.Fatal("other-base PR")
	}
	pr.BaseRef = ""
	if engine.AdvisoryEligible(pr) {
		t.Fatal("unknown-base PR")
	}
	pr.ItemKind = "run"
	pr.BaseRef = "main"
	if engine.AdvisoryEligible(pr) {
		t.Fatal("run item")
	}
}

// A pull request listed without its base (thin list) must not take a
// catch-up slot from eligible work (#386).
func TestCatchUpAdvisoryBatchSkipsPullsWithUnknownBase(t *testing.T) {
	pol, err := policy.Parse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	var open []snapshot.Item
	for i := 1; i <= 2; i++ {
		thin := snapshot.Item{Repo: "example/test-repo", Item: i, ItemKind: "pull", State: "open", CreatedAt: "2025-01-01T00:00:00Z"}
		open = append(open, thin)
	}
	open = append(open, issue(3), issue(4))
	got := engine.CatchUpAdvisoryBatch(open, pol)
	if len(got) != 2 || got[0].Item != 3 || got[1].Item != 4 {
		t.Fatalf("batch %+v", got)
	}
}

func TestCatchUpAdvisoryBatchCapsOldestFirst(t *testing.T) {
	pol, err := policy.Parse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	var open []snapshot.Item
	for i := 1; i <= 5; i++ {
		it := issue(i)
		it.CreatedAt = "2026-01-0" + string(rune('0'+i)) + "T00:00:00Z"
		open = append(open, it)
	}
	draft := issue(9)
	draft.ItemKind = "pull"
	draft.Draft = true
	draft.BaseRef = "main"
	open = append(open, draft)
	got := engine.CatchUpAdvisoryBatch(open, pol)
	if len(got) != 2 {
		t.Fatalf("cap %d", len(got))
	}
	if got[0].Item != 1 || got[1].Item != 2 {
		t.Fatalf("order %d %d", got[0].Item, got[1].Item)
	}
}

func TestAdmitSkipsDraftPullRequest(t *testing.T) {
	h := setup(t)
	it := issue(4)
	it.ItemKind = "pull"
	it.Draft = true
	it.DefaultBranch = "main"
	it.BaseRef = "main"
	it.HeadSHA = "h"
	h.f.Put(it)
	if err := h.e.IngestWebhook("d1", it.Repo, it.Item, it.ItemKind); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.StepRefresh(); err != nil {
		t.Fatal(err)
	}
	j, _ := store.JobState(h.st, it.Repo, it.Item)
	if j != nil {
		t.Fatal("draft admitted")
	}
}

func TestAdmitSkipsClosedPullRequest(t *testing.T) {
	h := setup(t)
	it := issue(8)
	it.ItemKind = "pull"
	it.State = "closed"
	it.Merged = true
	it.DefaultBranch = "main"
	it.BaseRef = "main"
	it.HeadSHA = "h"
	h.f.Put(it)
	if err := h.e.IngestWebhook("d-closed", it.Repo, it.Item, it.ItemKind); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.StepRefresh(); err != nil {
		t.Fatal(err)
	}
	j, _ := store.JobState(h.st, it.Repo, it.Item)
	if j != nil {
		t.Fatal("closed PR admitted")
	}
}

func TestRequestReviewRejectsIneligiblePull(t *testing.T) {
	h := setup(t)
	it := issue(5)
	it.ItemKind = "pull"
	it.Draft = true
	it.DefaultBranch = "main"
	it.BaseRef = "main"
	it.HeadSHA = "h"
	h.f.Put(it)
	_, err := h.e.RequestReview(it.Repo, it.Item)
	if !errors.Is(err, engine.ErrReviewIneligible) {
		t.Fatalf("err %v", err)
	}
}

func TestCatchUpOpenAndLocalUsesAdvisoryBatch(t *testing.T) {
	h := setup(t)
	var open []snapshot.Item
	for i := 1; i <= 4; i++ {
		it := issue(i)
		it.CreatedAt = "2026-02-0" + string(rune('0'+i)) + "T00:00:00Z"
		h.f.Put(it)
		open = append(open, it)
	}
	if err := h.e.CatchUpOpenAndLocal(open); err != nil {
		t.Fatal(err)
	}
	for {
		ok, err := h.e.StepRefresh()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
	}
	var n int
	if err := h.st.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE lane='review'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("jobs %d want 2", n)
	}
}

func TestProtectedLabelStillAdmitted(t *testing.T) {
	h := setup(t)
	it := issue(6)
	it.Labels = []string{"rusui:hold", "security"}
	h.putRefresh(it)
	if _, err := h.e.Claim("example/test-repo"); err != nil {
		t.Fatal(err)
	}
}

// Successive catch-up passes reach new items instead of re-selecting the
// oldest ones that already have a job for their content; a change on
// GitHub makes an item eligible again (#400).
func TestCatchUpPassesReachNewItems(t *testing.T) {
	h := setup(t)
	var open []snapshot.Item
	for i := 1; i <= 5; i++ {
		it := issue(i)
		it.CreatedAt = "2026-02-0" + string(rune('0'+i)) + "T00:00:00Z"
		h.f.Put(it)
		open = append(open, it)
	}
	pass := func() []int {
		t.Helper()
		if err := h.e.CatchUpOpenAndLocal(open); err != nil {
			t.Fatal(err)
		}
		for {
			ok, err := h.e.StepRefresh()
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				break
			}
		}
		rows, err := h.st.DB.Query(`SELECT item FROM jobs WHERE lane='review' ORDER BY item`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var items []int
		for rows.Next() {
			var n int
			_ = rows.Scan(&n)
			items = append(items, n)
		}
		return items
	}
	if got := pass(); fmt.Sprint(got) != "[1 2]" {
		t.Fatalf("first pass %v", got)
	}
	if got := pass(); fmt.Sprint(got) != "[1 2 3 4]" {
		t.Fatalf("second pass %v", got)
	}
	changed := open[0]
	changed.UpdatedAt = "2026-03-01T00:00:00Z"
	changed.Body = "new repro"
	h.f.Put(changed)
	open[0] = changed
	var before int
	_ = h.st.DB.QueryRow(`SELECT pending_revision FROM jobs WHERE item=1`).Scan(&before)
	pass()
	var after int
	_ = h.st.DB.QueryRow(`SELECT pending_revision FROM jobs WHERE item=1`).Scan(&after)
	if after <= before {
		t.Fatalf("changed item not requeued: pending %d -> %d", before, after)
	}
}

package engine_test

import (
	"errors"
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
	if !engine.AdvisoryEligible(pr) {
		t.Fatal("merged default-branch PR")
	}
	pr.State = "open"
	pr.Merged = false
	pr.BaseRef = "release-1"
	if engine.AdvisoryEligible(pr) {
		t.Fatal("other-base PR")
	}
	pr.ItemKind = "run"
	pr.BaseRef = "main"
	if engine.AdvisoryEligible(pr) {
		t.Fatal("run item")
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

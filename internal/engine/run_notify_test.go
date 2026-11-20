package engine_test

import (
	"fmt"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
)

func TestStartRunNotifiesOnce(t *testing.T) {
	h := setup(t)
	var starts []string
	h.e.NotifyRunStart = func(id int64, project string) {
		starts = append(starts, fmt.Sprintf("%d:%s", id, project))
	}
	id, err := h.e.StartRun("test", "do the thing", "idem-notify")
	if err != nil || id == 0 {
		t.Fatalf("%d %v", id, err)
	}
	if len(starts) != 1 || starts[0] != fmt.Sprintf("%d:test", id) {
		t.Fatalf("start %v", starts)
	}
	id2, err := h.e.StartRun("test", "ignored", "idem-notify")
	if err != nil || id2 != id {
		t.Fatalf("idem %d %d %v", id, id2, err)
	}
	if len(starts) != 1 {
		t.Fatalf("idempotent start notified again %v", starts)
	}
}

func TestStartTaskNotifiesOnce(t *testing.T) {
	h := setup(t)
	var starts []int64
	h.e.NotifyRunStart = func(id int64, project string) {
		if project != "test" {
			t.Errorf("project %s", project)
		}
		starts = append(starts, id)
	}
	a, err := h.e.StartTask("test", pin())
	if err != nil || a == nil {
		t.Fatalf("%+v %v", a, err)
	}
	if len(starts) != 1 || starts[0] != a.SessionID {
		t.Fatalf("start %v want %d", starts, a.SessionID)
	}
	b, err := h.e.StartTask("test", pin())
	if err != nil || b.SessionID != a.SessionID {
		t.Fatalf("reuse %+v %+v %v", a, b, err)
	}
	if len(starts) != 1 {
		t.Fatalf("reused task notified again %v", starts)
	}
}

func TestRunCompleteNotifiesTerminal(t *testing.T) {
	h := setup(t)
	var ends []string
	h.e.NotifyRunEnd = func(id int64, state string) {
		ends = append(ends, fmt.Sprintf("%d:%s", id, state))
	}
	sid, c := claimLeased(t, h)
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%d:completed", sid)
	if len(ends) != 1 || ends[0] != want {
		t.Fatalf("end %v want %s", ends, want)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	if len(ends) != 1 {
		t.Fatalf("replay complete notified %v", ends)
	}
}

func TestRunNotifyWaitsUntilFollowUpDrains(t *testing.T) {
	h := setup(t)
	var ends []string
	h.e.NotifyRunEnd = func(_ int64, state string) {
		ends = append(ends, state)
	}
	sid, c := claimLeased(t, h)
	if _, _, _, err := h.e.PromptQueued(sid, "after"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	if len(ends) != 0 {
		t.Fatalf("notified before drain %v", ends)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil {
		t.Fatalf("claim %+v %v", c2, err)
	}
	if _, err := h.e.Complete(c2.Job.ID, c2.Job.LeaseGeneration, c2.Job.ClaimedRevision, runArt(c2)); err != nil {
		t.Fatal(err)
	}
	if len(ends) != 1 || ends[0] != "completed" {
		t.Fatalf("end %v", ends)
	}
}

func TestRunFailNotifiesTerminal(t *testing.T) {
	h := setup(t)
	var ends []string
	h.e.NotifyRunEnd = func(_ int64, state string) {
		ends = append(ends, state)
	}
	_, c := claimLeased(t, h)
	for i := range engine.RetryLimit {
		if i > 0 {
			var err error
			c, err = h.e.Claim("example/test-repo")
			if err != nil || c == nil {
				t.Fatalf("retry %d claim %+v %v", i, c, err)
			}
		}
		if _, err := h.e.Fail(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision); err != nil {
			t.Fatal(err)
		}
		if i < engine.RetryLimit-1 && len(ends) != 0 {
			t.Fatalf("retry %d notified %v", i, ends)
		}
	}
	if len(ends) != 1 || ends[0] != "failed" {
		t.Fatalf("end %v", ends)
	}
}

func TestRunCancelNotifiesTerminal(t *testing.T) {
	h := setup(t)
	var ends []string
	h.e.NotifyRunEnd = func(_ int64, state string) {
		ends = append(ends, state)
	}
	sid, _ := claimLeased(t, h)
	if err := h.e.CancelSession(sid); err != nil {
		t.Fatal(err)
	}
	if len(ends) != 1 || ends[0] != "cancelled" {
		t.Fatalf("end %v", ends)
	}
}

func TestReviewCompleteDoesNotNotifyRun(t *testing.T) {
	h := setup(t)
	var n int
	h.e.NotifyRunStart = func(int64, string) { n++ }
	h.e.NotifyRunEnd = func(int64, string) { n++ }
	it := issue(1)
	h.f.Put(it)
	if err := h.e.CatchUpItem(it.Repo, it.Item, it.ItemKind); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.StepRefresh(); err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("review notified %d", n)
	}
}

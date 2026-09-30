package engine_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

// fakePublisher records the plane's pull-request writes.
type fakePublisher struct {
	specs []gh.PullSpec
	err   error
}

func (f *fakePublisher) PublishPull(repo string, s gh.PullSpec) (int, error) {
	f.specs = append(f.specs, s)
	if f.err != nil {
		return 0, f.err
	}
	return 7, nil
}

// completePublish completes the claimed turn with a publish request and
// returns the stored result.
func completePublish(t *testing.T, h *harn, c *engine.Claim, branch, candidate string) *engine.TaskResult {
	t.Helper()
	a := art(c, "keep", "", "")
	a.Result = &engine.TaskResult{
		SchemaVersion: engine.ResultSchema, SourceHash: c.ItemHash, CandidateSHA: candidate,
		Publish:   &engine.PublishRequest{Branch: branch, Title: "fix the pin"},
		Publisher: engine.PublisherPlane, // guest-sent; the plane decides
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	p, err := store.LatestReviewJSON(h.st, c.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got engine.Artifact
	if err := json.Unmarshal([]byte(p), &got); err != nil || got.Result == nil {
		t.Fatal(err)
	}
	return got.Result
}

func TestPlanePublishesImplementTurnAndUpdatesOnFollowUp(t *testing.T) {
	h := setupImplement(t)
	pub := &fakePublisher{}
	h.e.Publisher = pub
	task, err := h.e.StartTask("test", pin())
	if err != nil {
		t.Fatal(err)
	}
	branch := fmt.Sprintf("rusui/%d/pin", task.SessionID)
	h.f.Put(snapshot.Item{Repo: "example/test-repo", Item: 7, ItemKind: "pull", State: "open", HeadSHA: "sha-a"})

	r := completePublish(t, h, h.claim(), branch, "sha-a")
	if r.PullRequest != 7 || r.Publisher != engine.PublisherPlane || r.Outcome != engine.OutcomePublished {
		t.Fatalf("result %+v", r)
	}
	if len(pub.specs) != 1 || pub.specs[0] != (gh.PullSpec{Head: branch, SHA: "sha-a", Base: "main", Title: "fix the pin"}) {
		t.Fatalf("specs %+v", pub.specs)
	}

	if _, _, err := h.e.PromptFollowUp(task.SessionID, "narrow it"); err != nil {
		t.Fatal(err)
	}
	h.f.Put(snapshot.Item{Repo: "example/test-repo", Item: 7, ItemKind: "pull", State: "open", HeadSHA: "sha-b"})
	r = completePublish(t, h, h.claim(), branch, "sha-b")
	if r.Outcome != engine.OutcomePublished || len(pub.specs) != 2 || pub.specs[1].Number != 7 || pub.specs[1].SHA != "sha-b" {
		t.Fatalf("follow-up %+v %+v", r, pub.specs)
	}
}

func TestPlanePublicationRefusesWithoutWriting(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		task, off  bool
		candidate  string
		branch     func(sessionID int64) string
	}{
		{name: "no candidate commit", want: "candidate commit required", task: true, candidate: "-",
			branch: func(id int64) string { return fmt.Sprintf("rusui/%d/x", id) }},
		{name: "ordinary run session", want: "not an implement session",
			branch: func(id int64) string { return fmt.Sprintf("rusui/%d/x", id) }},
		{name: "foreign branch", want: "branch must be under", task: true,
			branch: func(int64) string { return "main" }},
		{name: "other session prefix", want: "branch must be under", task: true,
			branch: func(id int64) string { return fmt.Sprintf("rusui/%d/x", id+100) }},
		{name: "plane publication off", want: "plane publication is off", task: true, off: true,
			branch: func(id int64) string { return fmt.Sprintf("rusui/%d/x", id) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := setupImplement(t)
			pub := &fakePublisher{}
			if !tc.off {
				h.e.Publisher = pub
			}
			var sid int64
			if tc.task {
				task, err := h.e.StartTask("test", pin())
				if err != nil {
					t.Fatal(err)
				}
				sid = task.SessionID
			} else {
				var err error
				if sid, err = h.e.StartRun("test", "do the thing", ""); err != nil {
					t.Fatal(err)
				}
			}
			candidate := "sha-a"
			if tc.candidate == "-" {
				candidate = ""
			}
			r := completePublish(t, h, h.claim(), tc.branch(sid), candidate)
			if r.Outcome != engine.OutcomeBlocked || !strings.Contains(r.BlockedReason, tc.want) || r.Publisher != "" || r.PullRequest != 0 {
				t.Fatalf("result %+v", r)
			}
			if len(pub.specs) != 0 {
				t.Fatalf("wrote %+v", pub.specs)
			}
		})
	}
}

func TestPlanePublicationSkipsStaleLease(t *testing.T) {
	h := setupImplement(t)
	pub := &fakePublisher{}
	h.e.Publisher = pub
	task, err := h.e.StartTask("test", pin())
	if err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	a := art(c, "keep", "", "")
	a.Result = &engine.TaskResult{SchemaVersion: engine.ResultSchema, SourceHash: c.ItemHash, CandidateSHA: "sha-a",
		Publish: &engine.PublishRequest{Branch: fmt.Sprintf("rusui/%d/pin", task.SessionID), Title: "t"}}
	_, _ = h.e.Complete(c.Job.ID, c.Job.LeaseGeneration+1, c.Job.ClaimedRevision, a)
	if len(pub.specs) != 0 {
		t.Fatalf("stale lease wrote %+v", pub.specs)
	}
}

// Under plane publication a guest cannot name a pull request: one whose
// head it copied is not recorded, so no later follow-up edits it.
func TestPlanePublicationRefusesGuestClaimedPullRequest(t *testing.T) {
	h := setupImplement(t)
	pub := &fakePublisher{}
	h.e.Publisher = pub
	task, err := h.e.StartTask("test", pin())
	if err != nil {
		t.Fatal(err)
	}
	h.f.Put(snapshot.Item{Repo: "example/test-repo", Item: 3, ItemKind: "pull", State: "open", HeadSHA: "sha-a"})
	c := h.claim()
	a := art(c, "keep", "", "")
	a.Result = &engine.TaskResult{SchemaVersion: engine.ResultSchema, SourceHash: c.ItemHash, CandidateSHA: "sha-a", PullRequest: 3}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := engine.SessionPublication(h.st, task.SessionID); ok {
		t.Fatal("guest claim recorded as a publication")
	}

	if _, _, err := h.e.PromptFollowUp(task.SessionID, "again"); err != nil {
		t.Fatal(err)
	}
	completePublish(t, h, h.claim(), fmt.Sprintf("rusui/%d/pin", task.SessionID), "sha-b")
	if len(pub.specs) != 1 || pub.specs[0].Number != 0 {
		t.Fatalf("follow-up targeted a guest-named pull request: %+v", pub.specs)
	}
}

func TestPlaneFollowUpMustReuseItsBranch(t *testing.T) {
	h := setupImplement(t)
	pub := &fakePublisher{}
	h.e.Publisher = pub
	task, err := h.e.StartTask("test", pin())
	if err != nil {
		t.Fatal(err)
	}
	h.f.Put(snapshot.Item{Repo: "example/test-repo", Item: 7, ItemKind: "pull", State: "open", HeadSHA: "sha-a"})
	completePublish(t, h, h.claim(), fmt.Sprintf("rusui/%d/pin", task.SessionID), "sha-a")
	if _, _, err := h.e.PromptFollowUp(task.SessionID, "again"); err != nil {
		t.Fatal(err)
	}
	r := completePublish(t, h, h.claim(), fmt.Sprintf("rusui/%d/other", task.SessionID), "sha-b")
	if r.Outcome != engine.OutcomeBlocked || !strings.Contains(r.BlockedReason, "follow-up must publish branch") || len(pub.specs) != 1 {
		t.Fatalf("result %+v specs %+v", r, pub.specs)
	}
}

// A Complete retried after a lost response returns the stored receipt and
// does not write to GitHub again.
func TestPlanePublicationWritesOncePerCompletedTurn(t *testing.T) {
	h := setupImplement(t)
	pub := &fakePublisher{}
	h.e.Publisher = pub
	task, err := h.e.StartTask("test", pin())
	if err != nil {
		t.Fatal(err)
	}
	h.f.Put(snapshot.Item{Repo: "example/test-repo", Item: 7, ItemKind: "pull", State: "open", HeadSHA: "sha-a"})
	c := h.claim()
	a := art(c, "keep", "", "")
	a.Result = &engine.TaskResult{SchemaVersion: engine.ResultSchema, SourceHash: c.ItemHash, CandidateSHA: "sha-a",
		Publish: &engine.PublishRequest{Branch: fmt.Sprintf("rusui/%d/pin", task.SessionID), Title: "t"}}
	first, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a)
	if err != nil {
		t.Fatal(err)
	}
	again, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a)
	if err != nil || fmt.Sprint(again) != fmt.Sprint(first) {
		t.Fatalf("retry %v %v", again, err)
	}
	if len(pub.specs) != 1 {
		t.Fatalf("wrote %d times", len(pub.specs))
	}
}

// A branch that moved off the candidate is refused, not published.
func TestPlanePublicationBlocksWhenHeadIsNotTheCandidate(t *testing.T) {
	h := setupImplement(t)
	h.e.Publisher = &fakePublisher{err: fmt.Errorf("wrap: %w", gh.ErrHeadMismatch)}
	task, err := h.e.StartTask("test", pin())
	if err != nil {
		t.Fatal(err)
	}
	r := completePublish(t, h, h.claim(), fmt.Sprintf("rusui/%d/pin", task.SessionID), "sha-a")
	if r.Outcome != engine.OutcomeBlocked || !strings.Contains(r.BlockedReason, "not the candidate commit") || r.PullRequest != 0 {
		t.Fatalf("result %+v", r)
	}
}

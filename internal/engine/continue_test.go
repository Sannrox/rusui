package engine_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/publish"
	"github.com/sannrox/rusui/internal/store"
)

const implementFixture = `version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
  review: true
  comments: false
  close: false
  implement: true
  land: false
  max_reviews_per_repo_per_utc_day: 50
projects:
  test:
    repos:
      example/test-repo:
        visibility: public
        review: true
        implement: true
`

func setupImplement(t *testing.T) *harn {
	t.Helper()
	h := setup(t)
	pol, err := policy.Parse([]byte(implementFixture))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(pol)
	return h
}

func proveSHA(t *testing.T, h *harn, sessID int64, blob, snap string) *engine.VerifyReport {
	t.Helper()
	h.e.Container = env.Container{RT: &env.FakeRuntime{}, Image: "rusui-guest:test"}
	sha := blobSHA(blob)
	rep, err := h.e.Verify(engine.VerifySpec{
		Source:    publish.Source{Repo: "example/test-repo", Item: 1, SnapshotHash: snap, MainSHA: "aaa"},
		Candidate: publish.Candidate{SessionID: sessID, CommitSHA: sha, TreeSHA: "t", Ref: fmt.Sprintf("refs/heads/rusui/%d/work", sessID)},
		Author:    sessID, Reviewer: sessID + 1, Command: []string{"true"}, ChecksOn: true, CandidateBlob: blob,
	})
	if err != nil || rep.Outcome != publish.Proven {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, err := h.e.IndependentReview(engine.ReviewInput{
		ProofID: rep.ID, AuthorSessionID: sessID, ReviewerSessionID: sessID + 1,
		CandidateSHA: sha, Disposition: "accept",
	}); err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestFeedbackDuplicateAndOutOfOrder(t *testing.T) {
	h := setupImplement(t)
	task, err := h.e.StartTask("test", pin())
	if err != nil {
		t.Fatal(err)
	}
	in := engine.FeedbackInput{EffortKey: task.EffortKey, Seq: 1, CandidateSHA: "sha-a", Prompt: "do it"}
	a, err := h.e.ApplyFeedback(in)
	if err != nil || !a.Applied {
		t.Fatalf("%+v %v", a, err)
	}
	dup, err := h.e.ApplyFeedback(in)
	if err != nil || dup.Applied || dup.ID != a.ID {
		t.Fatalf("dup %+v %v", dup, err)
	}
	clash := in
	clash.Prompt = "other"
	if _, err := h.e.ApplyFeedback(clash); !errors.Is(err, engine.ErrFeedbackClash) {
		t.Fatalf("clash %v", err)
	}
	if _, err := h.e.ApplyFeedback(engine.FeedbackInput{EffortKey: task.EffortKey, Seq: 3, CandidateSHA: "sha-a", Prompt: "skip"}); !errors.Is(err, engine.ErrOutOfOrder) {
		t.Fatalf("ooo %v", err)
	}
}

func TestFeedbackBaseDriftInvalidatesProof(t *testing.T) {
	h := setupImplement(t)
	task, err := h.e.StartTask("test", pin())
	if err != nil {
		t.Fatal(err)
	}
	rep := proveSHA(t, h, task.SessionID, "patch-1", "snap-1")
	if _, err := h.e.ApplyFeedback(engine.FeedbackInput{EffortKey: task.EffortKey, Seq: 1, CandidateSHA: blobSHA("patch-1"), Prompt: "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.ApplyFeedback(engine.FeedbackInput{EffortKey: task.EffortKey, Seq: 2, CandidateSHA: blobSHA("patch-2"), Prompt: "base moved"}); err != nil {
		t.Fatal(err)
	}
	p, err := store.GetProof(h.st, rep.ID)
	if err != nil || p.Outcome != string(publish.SourceDrift) {
		t.Fatalf("proof %+v %v", p, err)
	}
}

func TestAbandonedEffortRejectsFeedback(t *testing.T) {
	h := setupImplement(t)
	task, err := h.e.StartTask("test", pin())
	if err != nil {
		t.Fatal(err)
	}
	if err := h.e.AbandonEffort(task.EffortKey); err != nil {
		t.Fatal(err)
	}
	_, err = h.e.ApplyFeedback(engine.FeedbackInput{EffortKey: task.EffortKey, Seq: 1, CandidateSHA: "sha-a", Prompt: "late"})
	if !errors.Is(err, engine.ErrEffortClosed) {
		t.Fatalf("feedback after abandon %v", err)
	}
}

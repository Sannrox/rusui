package engine_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/publish"
	"github.com/sannrox/rusui/internal/store"
)

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

func TestFeedbackTwoRoundsOnePR(t *testing.T) {
	h, pub := setupPublish(t)
	task, err := h.e.StartTask("test", pin())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, task.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	rep := proveSHA(t, h, task.SessionID, "patch-1", "snap-1")
	pub.OpenHeadSHA = blobSHA("patch-1")
	req := engine.PublishRequest{
		Source:    publish.Source{Repo: task.Repo, Item: sess.Item, SnapshotHash: "snap-1", MainSHA: "aaa"},
		Candidate: publish.Candidate{SessionID: task.SessionID, CommitSHA: blobSHA("patch-1"), TreeSHA: "t", Ref: fmt.Sprintf("refs/heads/rusui/%d/work", task.SessionID)},
		ProofID:   rep.ID,
	}
	first, err := h.e.ContinuePublish(task.EffortKey, req)
	if err != nil || first.State != engine.PubSucceeded {
		t.Fatalf("%+v %v", first, err)
	}
	if _, err := h.e.ApplyFeedback(engine.FeedbackInput{EffortKey: task.EffortKey, Seq: 1, CandidateSHA: blobSHA("patch-1"), Prompt: "round 1"}); err != nil {
		t.Fatal(err)
	}
	rep2 := proveSHA(t, h, task.SessionID, "patch-2", "snap-2")
	if _, err := h.e.ApplyFeedback(engine.FeedbackInput{EffortKey: task.EffortKey, Seq: 2, CandidateSHA: blobSHA("patch-2"), Prompt: "round 2"}); err != nil {
		t.Fatal(err)
	}
	pub.OpenHeadSHA = blobSHA("patch-2")
	req.Source.SnapshotHash = "snap-2"
	req.Candidate.CommitSHA = blobSHA("patch-2")
	req.ProofID = rep2.ID
	pub.SetHead(task.Repo, req.Candidate.Ref, blobSHA("patch-2"))
	second, err := h.e.ContinuePublish(task.EffortKey, req)
	if err != nil || second.State != engine.PubSucceeded {
		t.Fatalf("%+v %v", second, err)
	}
	if pub.OpenCount() != 1 || second.PR.Number != first.PR.Number {
		t.Fatalf("expected one PR, opens=%d %d vs %d", pub.OpenCount(), first.PR.Number, second.PR.Number)
	}
}

func TestFeedbackDuplicateAndOutOfOrder(t *testing.T) {
	h, _ := setupPublish(t)
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
	h, pub := setupPublish(t)
	task, err := h.e.StartTask("test", pin())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, task.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	rep := proveSHA(t, h, task.SessionID, "patch-1", "snap-1")
	pub.OpenHeadSHA = blobSHA("patch-1")
	req := engine.PublishRequest{
		Source:    publish.Source{Repo: task.Repo, Item: sess.Item, SnapshotHash: "snap-1", MainSHA: "aaa"},
		Candidate: publish.Candidate{SessionID: task.SessionID, CommitSHA: blobSHA("patch-1"), TreeSHA: "t", Ref: fmt.Sprintf("refs/heads/rusui/%d/work", task.SessionID)},
		ProofID:   rep.ID,
	}
	if _, err := h.e.ContinuePublish(task.EffortKey, req); err != nil {
		t.Fatal(err)
	}
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
	got, err := h.e.ContinuePublish(task.EffortKey, req)
	if err != nil || got.State != engine.PubDenied {
		t.Fatalf("stale publish %+v %v", got, err)
	}
}

func TestAbandonAndCleanupOwnedRef(t *testing.T) {
	h, pub := setupPublish(t)
	task, err := h.e.StartTask("test", pin())
	if err != nil {
		t.Fatal(err)
	}
	if err := h.e.AbandonEffort(task.EffortKey); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.ContinuePublish(task.EffortKey, engine.PublishRequest{}); !errors.Is(err, engine.ErrEffortClosed) {
		t.Fatalf("publish after abandon %v", err)
	}
	owned := fmt.Sprintf("refs/heads/rusui/%d/work", task.SessionID)
	if err := h.e.CleanupOwnedRefs(task.EffortKey, owned); err != nil {
		t.Fatal(err)
	}
	if len(pub.Deleted) != 1 || pub.Deleted[0] != task.Repo+"#"+owned {
		t.Fatalf("deleted %+v", pub.Deleted)
	}
	if err := h.e.CleanupOwnedRefs(task.EffortKey, "refs/heads/main"); !errors.Is(err, engine.ErrRefNotOwned) {
		t.Fatalf("main %v", err)
	}
	if err := h.e.CleanupOwnedRefs(task.EffortKey, "refs/heads/rusui/999/work"); !errors.Is(err, engine.ErrRefNotOwned) {
		t.Fatalf("other session %v", err)
	}
}

func TestContinuePublishInterruptionReconcilesOnePR(t *testing.T) {
	h, pub := setupPublish(t)
	task, err := h.e.StartTask("test", pin())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, task.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	rep := proveSHA(t, h, task.SessionID, "patch-1", "snap-1")
	pub.OpenHeadSHA = blobSHA("patch-1")
	pub.AfterOpen = func(*gh.PullRequest) error { return errors.New("timeout") }
	req := engine.PublishRequest{
		Source:    publish.Source{Repo: task.Repo, Item: sess.Item, SnapshotHash: "snap-1", MainSHA: "aaa"},
		Candidate: publish.Candidate{SessionID: task.SessionID, CommitSHA: blobSHA("patch-1"), TreeSHA: "t", Ref: fmt.Sprintf("refs/heads/rusui/%d/work", task.SessionID)},
		ProofID:   rep.ID,
	}
	got, err := h.e.ContinuePublish(task.EffortKey, req)
	if err != nil || got.State != engine.PubSucceeded {
		t.Fatalf("%+v %v", got, err)
	}
	again, err := h.e.ContinuePublish(task.EffortKey, req)
	if err != nil || again.State != engine.PubSucceeded || again.PR.Number != got.PR.Number {
		t.Fatalf("%+v %v", again, err)
	}
	if pub.OpenCount() != 1 {
		t.Fatalf("opens %d", pub.OpenCount())
	}
}

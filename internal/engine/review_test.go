package engine_test

import (
	"errors"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/publish"
)

func provenProof(t *testing.T, h *harn) *engine.VerifyReport {
	t.Helper()
	h.e.Container = env.Container{RT: &env.FakeRuntime{}, Image: "rusui-guest:test"}
	blob := "patch-ok"
	rep, err := h.e.Verify(engine.VerifySpec{
		Source:    publish.Source{Repo: "example/test-repo", Item: 1, SnapshotHash: "snap", MainSHA: "main"},
		Candidate: publish.Candidate{SessionID: 7, CommitSHA: blobSHA(blob), TreeSHA: "t", Ref: "refs/heads/rusui/7/work"},
		Author:    7, Reviewer: 8, Command: []string{"true"}, ChecksOn: true, CandidateBlob: blob,
	})
	if err != nil || rep.Outcome != publish.Proven {
		t.Fatalf("%+v %v", rep, err)
	}
	return rep
}

func TestIndependentReviewAcceptsProvenCandidate(t *testing.T) {
	h := setup(t)
	rep := provenProof(t, h)
	id, err := h.e.IndependentReview(engine.ReviewInput{
		ProofID: rep.ID, AuthorSessionID: 7, ReviewerSessionID: 8,
		CandidateSHA: blobSHA("patch-ok"), Disposition: "accept",
	})
	if err != nil || id == 0 {
		t.Fatalf("%d %v", id, err)
	}
	ok, err := h.e.PublicationReady(blobSHA("patch-ok"), rep.ID)
	if err != nil || !ok {
		t.Fatalf("ready %v %v", ok, err)
	}
}

func TestIndependentReviewFailClosed(t *testing.T) {
	h := setup(t)
	rep := provenProof(t, h)
	sha := blobSHA("patch-ok")
	if _, err := h.e.IndependentReview(engine.ReviewInput{
		ProofID: rep.ID, AuthorSessionID: 7, ReviewerSessionID: 7,
		CandidateSHA: sha, Disposition: "accept",
	}); !errors.Is(err, engine.ErrSelfReview) {
		t.Fatalf("self %v", err)
	}
	if _, err := h.e.IndependentReview(engine.ReviewInput{
		ProofID: 0, AuthorSessionID: 7, ReviewerSessionID: 8,
		CandidateSHA: sha, Disposition: "accept",
	}); !errors.Is(err, engine.ErrNoProof) {
		t.Fatalf("missing proof %v", err)
	}
	if _, err := h.e.IndependentReview(engine.ReviewInput{
		ProofID: rep.ID, AuthorSessionID: 7, ReviewerSessionID: 8,
		CandidateSHA: sha, Disposition: "accept", RepairCount: publish.MaxRepair + 1,
	}); !errors.Is(err, engine.ErrRepairLimit) {
		t.Fatalf("repairs %v", err)
	}
	ok, err := h.e.PublicationReady(sha, rep.ID)
	if err != nil || ok {
		t.Fatalf("unreviewed ready %v %v", ok, err)
	}
}

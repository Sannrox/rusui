package engine_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/publish"
	"github.com/sannrox/rusui/internal/store"
)

const publishFixture = `version: 2
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

func setupPublish(t *testing.T) (*harn, *gh.FakePublisher) {
	t.Helper()
	h := setup(t)
	pol, err := policy.Parse([]byte(publishFixture))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(pol)
	pub := gh.NewFakePublisher()
	h.e.Publisher = pub
	return h, pub
}

func readyCandidate(t *testing.T, h *harn) (engine.PublishRequest, *engine.VerifyReport) {
	t.Helper()
	h.e.Container = env.Container{RT: &env.FakeRuntime{}, Image: "rusui-guest:test"}
	blob := "patch-ok"
	sha := blobSHA(blob)
	rep, err := h.e.Verify(engine.VerifySpec{
		Source:    publish.Source{Repo: "example/test-repo", Item: 1, SnapshotHash: "snap", MainSHA: "main"},
		Candidate: publish.Candidate{SessionID: 7, CommitSHA: sha, TreeSHA: "t", Ref: "refs/heads/rusui/7/work"},
		Author:    7, Reviewer: 8, Command: []string{"true"}, ChecksOn: true, CandidateBlob: blob,
	})
	if err != nil || rep.Outcome != publish.Proven {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, err := h.e.IndependentReview(engine.ReviewInput{
		ProofID: rep.ID, AuthorSessionID: 7, ReviewerSessionID: 8,
		CandidateSHA: sha, Disposition: "accept",
	}); err != nil {
		t.Fatal(err)
	}
	req := engine.PublishRequest{
		Source:    publish.Source{Repo: "example/test-repo", Item: 1, SnapshotHash: "snap", MainSHA: "main"},
		Candidate: publish.Candidate{SessionID: 7, CommitSHA: sha, TreeSHA: "t", Ref: "refs/heads/rusui/7/work"},
		ProofID:   rep.ID,
		Action:    publish.ActionOpenPR,
	}
	return req, rep
}

func TestPublishOpensDraftPR(t *testing.T) {
	h, pub := setupPublish(t)
	req, _ := readyCandidate(t, h)
	pub.OpenHeadSHA = req.Candidate.CommitSHA
	got, err := h.e.Publish(req)
	if err != nil || got.State != engine.PubSucceeded || got.PR == nil || got.PR.Number == 0 {
		t.Fatalf("%+v %v", got, err)
	}
	if !got.PR.Draft {
		t.Fatal("must be draft")
	}
	if pub.OpenCount() != 1 {
		t.Fatalf("opens %d", pub.OpenCount())
	}
	if !strings.Contains(got.PR.Body, req.Candidate.CommitSHA) || !strings.Contains(got.PR.Body, "Human merge") {
		t.Fatalf("body %q", got.PR.Body)
	}
	if !strings.Contains(got.PR.Body, "refs/heads/rusui/7/work") || !strings.Contains(got.PR.Body, "example/test-repo") {
		t.Fatalf("provenance %q", got.PR.Body)
	}
	row, err := store.GetPublication(h.st, got.ActionID)
	if err != nil || row == nil || row.State != engine.PubSucceeded || row.PRNumber != got.PR.Number {
		t.Fatalf("%+v %v", row, err)
	}
}

func TestPublishTimeoutAfterRemoteSuccessReconciles(t *testing.T) {
	h, pub := setupPublish(t)
	req, _ := readyCandidate(t, h)
	pub.OpenHeadSHA = req.Candidate.CommitSHA
	pub.AfterOpen = func(*gh.PullRequest) error { return errors.New("timeout") }
	got, err := h.e.Publish(req)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != engine.PubSucceeded || got.PR == nil || got.PR.Number == 0 {
		t.Fatalf("reconcile %+v", got)
	}
	if pub.OpenCount() != 1 {
		t.Fatalf("opens %d", pub.OpenCount())
	}
}

func TestPublishRestartDoesNotDuplicate(t *testing.T) {
	h, pub := setupPublish(t)
	req, _ := readyCandidate(t, h)
	pub.OpenHeadSHA = req.Candidate.CommitSHA
	first, err := h.e.Publish(req)
	if err != nil || first.State != engine.PubSucceeded {
		t.Fatalf("%+v %v", first, err)
	}
	second, err := h.e.Publish(req)
	if err != nil || second.State != engine.PubSucceeded {
		t.Fatalf("%+v %v", second, err)
	}
	if pub.OpenCount() != 1 {
		t.Fatalf("duplicate open %d", pub.OpenCount())
	}
	if first.PR.Number != second.PR.Number {
		t.Fatalf("pr %d vs %d", first.PR.Number, second.PR.Number)
	}
}

func TestPublishDuplicateDeliveryIsNoop(t *testing.T) {
	h, pub := setupPublish(t)
	req, _ := readyCandidate(t, h)
	pub.OpenHeadSHA = req.Candidate.CommitSHA
	if _, err := h.e.Publish(req); err != nil {
		t.Fatal(err)
	}
	again, err := h.e.Publish(req)
	if err != nil || again.State != engine.PubSucceeded {
		t.Fatalf("%+v %v", again, err)
	}
	if pub.OpenCount() != 1 {
		t.Fatalf("opens %d", pub.OpenCount())
	}
}

func TestPublishStaleProofDenied(t *testing.T) {
	h, pub := setupPublish(t)
	req, _ := readyCandidate(t, h)
	req.Candidate.CommitSHA = "not-the-proven-sha"
	got, err := h.e.Publish(req)
	if err != nil || got.State != engine.PubDenied {
		t.Fatalf("%+v %v", got, err)
	}
	if pub.OpenCount() != 0 {
		t.Fatal("mutated on stale proof")
	}
}

func TestPublishChangedPolicyDenied(t *testing.T) {
	h, pub := setupPublish(t)
	req, _ := readyCandidate(t, h)
	pub.OpenHeadSHA = req.Candidate.CommitSHA
	off := strings.ReplaceAll(publishFixture, "implement: true", "implement: false")
	pol, err := policy.Parse([]byte(off))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(pol)
	got, err := h.e.Publish(req)
	if err != nil || got.State != engine.PubDenied {
		t.Fatalf("%+v %v", got, err)
	}
	if pub.OpenCount() != 0 {
		t.Fatal("mutated after policy change")
	}
}

func TestPublishCancelledDenied(t *testing.T) {
	h, pub := setupPublish(t)
	req, _ := readyCandidate(t, h)
	req.Cancelled = true
	got, err := h.e.Publish(req)
	if err != nil || got.State != engine.PubCancelled {
		t.Fatalf("%+v %v", got, err)
	}
	if pub.OpenCount() != 0 {
		t.Fatal("mutated when cancelled")
	}
}

func TestPublishUpdatePRNoop(t *testing.T) {
	h, pub := setupPublish(t)
	req, _ := readyCandidate(t, h)
	pub.OpenHeadSHA = req.Candidate.CommitSHA
	first, err := h.e.Publish(req)
	if err != nil || first.State != engine.PubSucceeded {
		t.Fatalf("%+v %v", first, err)
	}
	req.Action = publish.ActionUpdatePR
	got, err := h.e.Publish(req)
	if err != nil || got.State != engine.PubSucceeded || got.PR == nil || got.PR.Number != first.PR.Number {
		t.Fatalf("%+v %v", got, err)
	}
	if pub.OpenCount() != 1 {
		t.Fatalf("update must not open another PR, opens %d", pub.OpenCount())
	}
}

func TestPublishUpdatePRWithoutRemoteDenied(t *testing.T) {
	h, pub := setupPublish(t)
	req, _ := readyCandidate(t, h)
	req.Action = publish.ActionUpdatePR
	got, err := h.e.Publish(req)
	if err != nil || got.State != engine.PubDenied {
		t.Fatalf("%+v %v", got, err)
	}
	if pub.OpenCount() != 0 {
		t.Fatal("update_pr opened a PR")
	}
}

func TestPublishUsesRepoDefaultBranch(t *testing.T) {
	h, pub := setupPublish(t)
	req, _ := readyCandidate(t, h)
	pub.OpenHeadSHA = req.Candidate.CommitSHA
	pub.Default = "trunk"
	got, err := h.e.Publish(req)
	if err != nil || got.State != engine.PubSucceeded || got.PR == nil || got.PR.Base != "trunk" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestPublishUnnamedActionDenied(t *testing.T) {
	h, pub := setupPublish(t)
	req, _ := readyCandidate(t, h)
	req.Action = "merge"
	got, err := h.e.Publish(req)
	if err != nil || got.State != engine.PubDenied {
		t.Fatalf("%+v %v", got, err)
	}
	if pub.OpenCount() != 0 {
		t.Fatal("mutated unnamed action")
	}
}

func TestPublishRetryAfterUncertain(t *testing.T) {
	h, pub := setupPublish(t)
	req, _ := readyCandidate(t, h)
	pub.OpenHeadSHA = req.Candidate.CommitSHA
	pub.OpenErr = errors.New("timeout")
	got, err := h.e.Publish(req)
	if err == nil || got.State != engine.PubUncertain {
		t.Fatalf("%+v %v", got, err)
	}
	pub.OpenErr = nil
	if err := h.e.RetryPublications(); err != nil {
		t.Fatal(err)
	}
	row, err := store.GetPublication(h.st, got.ActionID)
	if err != nil || row == nil || row.State != engine.PubSucceeded {
		t.Fatalf("%+v %v", row, err)
	}
}

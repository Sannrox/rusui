package engine_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/publish"
	"github.com/sannrox/rusui/internal/store"
)

func blobSHA(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestVerifyProvesIsolatedCandidate(t *testing.T) {
	h := setup(t)
	h.e.Container = env.Container{RT: &env.FakeRuntime{}, Image: "rusui-guest:test"}
	blob := "patch-a"
	rep, err := h.e.Verify(engine.VerifySpec{
		Source:    publish.Source{Repo: "example/test-repo", Item: 1, SnapshotHash: "snap", MainSHA: "main"},
		Candidate: publish.Candidate{SessionID: 7, CommitSHA: blobSHA(blob), TreeSHA: "t", Ref: "refs/heads/rusui/7/work"},
		Author:    7, Reviewer: 8, Command: []string{"true"}, ChecksOn: true, CandidateBlob: blob,
	})
	if err != nil || rep.Outcome != publish.Proven || rep.ExitCode != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	got, err := store.GetProof(h.st, rep.ID)
	if err != nil || got.Outcome != string(publish.Proven) {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestVerifyByteChangeNotProven(t *testing.T) {
	h := setup(t)
	h.e.Container = env.Container{RT: &env.FakeRuntime{}, Image: "rusui-guest:test"}
	rep, err := h.e.Verify(engine.VerifySpec{
		Source:    publish.Source{Repo: "example/test-repo", Item: 1, SnapshotHash: "snap", MainSHA: "main"},
		Candidate: publish.Candidate{SessionID: 7, CommitSHA: blobSHA("patch-a"), TreeSHA: "t", Ref: "refs/heads/rusui/7/work"},
		Author:    7, Reviewer: 8, Command: []string{"true"}, ChecksOn: true, CandidateBlob: "patch-b",
	})
	if err != nil || rep.Outcome == publish.Proven {
		t.Fatalf("changed blob proven %+v %v", rep, err)
	}
}

func TestVerifyRequiresContainer(t *testing.T) {
	h := setup(t)
	_, err := h.e.Verify(engine.VerifySpec{
		Candidate:     publish.Candidate{CommitSHA: "abc"},
		CandidateBlob: "x",
	})
	if err == nil {
		t.Fatal("expected isolation required")
	}
}

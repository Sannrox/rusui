package engine_test

import (
	"testing"

	"github.com/sannrox/rusui/internal/engine"
)

func TestCompleteRunRejectsSyntheticKeep(t *testing.T) {
	h := setup(t)
	if _, err := h.e.StartRun("test", "do the thing", ""); err != nil {
		t.Fatal(err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err := h.st.DB.QueryRow(`SELECT kind FROM receipts WHERE job_id=?`, c.Job.ID).Scan(&kind); err != nil || kind != "fail" {
		t.Fatalf("synthetic keep receipt %s %v", kind, err)
	}
}

func TestCompleteRunAcceptsFindingResult(t *testing.T) {
	h := setup(t)
	if _, err := h.e.StartRun("test", "do the thing", ""); err != nil {
		t.Fatal(err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	a := art(c, "keep", "", "")
	a.Result = &engine.TaskResult{
		SchemaVersion: engine.ResultSchema,
		SourceHash:    c.ItemHash,
		Findings:      []engine.Finding{{Title: "fix", Body: "bounded patch"}},
	}
	out, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a)
	if err != nil {
		t.Fatal(err)
	}
	if out["kind"] != "complete" {
		t.Fatalf("%v", out)
	}
	if a.Result.Hash() == "" {
		t.Fatal("hash")
	}
}

func TestValidateResultRejectsAgentVerified(t *testing.T) {
	art := engine.Artifact{ItemKind: "run", SnapshotHash: "s", Result: &engine.TaskResult{
		SchemaVersion: 1, SourceHash: "s", Findings: []engine.Finding{{Title: "x"}}, AgentVerified: true,
	}}
	if err := engine.ValidateResult(art); err == nil {
		t.Fatal("agent verified")
	}
}

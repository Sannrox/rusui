package engine_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/core"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/store"
)

func interviewDraft(sessionID int64) *engine.DraftRequest {
	turn := func(n int) string {
		return fmt.Sprintf(`{"name":"m","owner":"sales","source_turn":"interview-%d#turn-%d"}`, sessionID, n)
	}
	return &engine.DraftRequest{
		Namespace:            "sales",
		BranchID:             fmt.Sprintf("interview-%d", sessionID),
		ParentRevisionDigest: "sha256:published",
		Members: []engine.DraftMember{
			{MemberKind: "object_type", MemberID: "Customer", DefinitionJSON: turn(1)},
			{MemberKind: "object_type", MemberID: "Order", DefinitionJSON: `{"name":"Order","owner":"sales","access_marking":"finance","source_turn":"` + fmt.Sprintf("interview-%d#turn-2", sessionID) + `"}`},
			{MemberKind: "link_type", MemberID: "PlacedBy", DefinitionJSON: turn(3)},
		},
	}
}

func completeDraft(t *testing.T, h *harn, c *engine.Claim, draft *engine.DraftRequest) *engine.TaskResult {
	t.Helper()
	a := art(c, "keep", "", "")
	a.Result = &engine.TaskResult{
		SchemaVersion: engine.ResultSchema, SourceHash: c.ItemHash,
		Draft: draft,
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

func TestInterviewTurnDraftsTypesAndLinksWithSourceTurn(t *testing.T) {
	h := setup(t)
	fake := &core.Fake{}
	h.e.Core = fake
	sid, err := h.e.StartRun("test", "describe the sales domain", "")
	if err != nil {
		t.Fatal(err)
	}
	r := completeDraft(t, h, h.claim(), interviewDraft(sid))
	if r.Outcome != engine.OutcomeProposed || r.Draft == nil {
		t.Fatalf("result %+v", r)
	}
	prop, err := store.LatestAction(h.st, sid, engine.ActionCoreDraft)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := h.e.ApplySessionDraft(sid)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Refused || rec.Namespace != "sales" || rec.CoreProposalID == "" || rec.Lint == nil {
		t.Fatalf("receipt %+v", rec)
	}
	creates, edits, lints, proposals, merges, approves := fake.Snapshot()
	if creates != 1 || edits != 1 || lints != 1 || proposals != 1 || merges != 0 || approves != 0 {
		t.Fatalf("core calls create=%d edit=%d lint=%d propose=%d merge=%d approve=%d", creates, edits, lints, proposals, merges, approves)
	}
	if len(fake.Edits) != 1 || len(fake.Edits[0].Upserts) != 3 {
		t.Fatalf("edits %+v", fake.Edits)
	}
	seen := map[string]string{}
	for _, m := range fake.Edits[0].Upserts {
		var body map[string]any
		if err := json.Unmarshal([]byte(m.DefinitionJSON), &body); err != nil {
			t.Fatal(err)
		}
		turn, _ := body["source_turn"].(string)
		if turn == "" {
			t.Fatalf("member %s missing source_turn", m.MemberID)
		}
		seen[m.MemberKind+"/"+m.MemberID] = turn
	}
	if seen["object_type/Customer"] != fmt.Sprintf("interview-%d#turn-1", sid) ||
		seen["object_type/Order"] != fmt.Sprintf("interview-%d#turn-2", sid) ||
		seen["link_type/PlacedBy"] != fmt.Sprintf("interview-%d#turn-3", sid) {
		t.Fatalf("source_turn %v", seen)
	}
	if fake.Lints[0].BranchID != r.Draft.BranchID {
		t.Fatalf("lint branch %s", fake.Lints[0].BranchID)
	}
	again, err := h.e.ApplySessionDraft(sid)
	if err != nil || again.ActionID != rec.ActionID || len(fake.Edits) != 1 {
		t.Fatalf("idempotent apply %+v %v edits %d", again, err, len(fake.Edits))
	}
	if prop.Type != engine.ActionCoreDraft {
		t.Fatalf("proposal %s", prop.Type)
	}
}

func TestInterviewFollowUpDraftReusesSessionBranch(t *testing.T) {
	h := setup(t)
	fake := &core.Fake{}
	h.e.Core = fake
	sid, err := h.e.StartRun("test", "describe the sales domain", "")
	if err != nil {
		t.Fatal(err)
	}
	if r := completeDraft(t, h, h.claim(), interviewDraft(sid)); r.Draft == nil {
		t.Fatalf("first %+v", r)
	}
	if _, err := h.e.ApplySessionDraft(sid); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.e.PromptFollowUp(sid, "add OrderStatus"); err != nil {
		t.Fatal(err)
	}
	second := interviewDraft(sid)
	second.Members = append(second.Members, engine.DraftMember{
		MemberKind: "object_type", MemberID: "OrderStatus",
		DefinitionJSON: fmt.Sprintf(`{"name":"OrderStatus","owner":"sales","source_turn":"interview-%d#turn-4"}`, sid),
	})
	if r := completeDraft(t, h, h.claim(), second); r.Draft == nil {
		t.Fatalf("second %+v", r)
	}
	rec, err := h.e.ApplySessionDraft(sid)
	if err != nil || rec.Refused {
		t.Fatalf("second apply %+v %v", rec, err)
	}
	creates, edits, _, proposals, merges, approves := fake.Snapshot()
	if creates != 1 || edits != 2 || proposals != 2 || merges != 0 || approves != 0 {
		t.Fatalf("core calls create=%d edit=%d propose=%d merge=%d approve=%d", creates, edits, proposals, merges, approves)
	}
}

func TestDraftApplyRecoversAfterLintFailure(t *testing.T) {
	h := setup(t)
	fake := &core.Fake{FailLint: 1}
	h.e.Core = fake
	sid, err := h.e.StartRun("test", "describe the sales domain", "")
	if err != nil {
		t.Fatal(err)
	}
	if r := completeDraft(t, h, h.claim(), interviewDraft(sid)); r.Draft == nil {
		t.Fatalf("result %+v", r)
	}
	if _, err := h.e.ApplySessionDraft(sid); err == nil {
		t.Fatal("expected lint failure")
	}
	creates, edits, lints, proposals, _, _ := fake.Snapshot()
	if creates != 1 || edits != 1 || lints != 0 || proposals != 0 {
		t.Fatalf("after lint fail create=%d edit=%d lint=%d propose=%d", creates, edits, lints, proposals)
	}
	rec, err := h.e.ApplySessionDraft(sid)
	if err != nil || rec.Refused || rec.CoreProposalID == "" {
		t.Fatalf("retry %+v %v", rec, err)
	}
	creates, edits, lints, proposals, merges, approves := fake.Snapshot()
	if creates != 1 || edits != 1 || lints != 1 || proposals != 1 || merges != 0 || approves != 0 {
		t.Fatalf("recovered create=%d edit=%d lint=%d propose=%d merge=%d approve=%d", creates, edits, lints, proposals, merges, approves)
	}
}

func TestDraftApplyRefusesLintFromAnotherHead(t *testing.T) {
	h := setup(t)
	fake := &core.Fake{FailLint: 1}
	h.e.Core = fake
	sid, err := h.e.StartRun("test", "describe the sales domain", "")
	if err != nil {
		t.Fatal(err)
	}
	if r := completeDraft(t, h, h.claim(), interviewDraft(sid)); r.Draft == nil {
		t.Fatalf("result %+v", r)
	}
	if _, err := h.e.ApplySessionDraft(sid); err == nil {
		t.Fatal("expected lint failure")
	}
	fake.SetHead("sales", fmt.Sprintf("interview-%d", sid), "sha256:other-author")
	rec, err := h.e.ApplySessionDraft(sid)
	if err == nil || rec.CoreProposalID != "" {
		t.Fatalf("retry %+v %v", rec, err)
	}
	if !strings.Contains(err.Error(), "does not match candidate") {
		t.Fatalf("err %v", err)
	}
	creates, edits, lints, proposals, merges, approves := fake.Snapshot()
	if creates != 1 || edits != 1 || lints != 1 || proposals != 0 || merges != 0 || approves != 0 {
		t.Fatalf("create=%d edit=%d lint=%d propose=%d merge=%d approve=%d", creates, edits, lints, proposals, merges, approves)
	}
}

func TestDraftPermissionDeniedIsSessionVisible(t *testing.T) {
	h := setup(t)
	fake := &core.Fake{Denied: core.DeniedError{Message: "namespace write required"}}
	h.e.Core = fake
	sid, err := h.e.StartRun("test", "describe the sales domain", "")
	if err != nil {
		t.Fatal(err)
	}
	if r := completeDraft(t, h, h.claim(), interviewDraft(sid)); r.Outcome != engine.OutcomeProposed {
		t.Fatalf("result %+v", r)
	}
	rec, err := h.e.ApplySessionDraft(sid)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Refused || rec.Code != "permission_denied" || rec.Message == "" {
		t.Fatalf("receipt %+v", rec)
	}
	got, ok, err := engine.SessionDraft(h.st, sid)
	if err != nil || !ok || !got.Refused || got.Code != "permission_denied" {
		t.Fatalf("session draft %+v ok=%v err=%v", got, ok, err)
	}
	creates, _, _, _, merges, _ := fake.Snapshot()
	if creates != 0 || merges != 0 {
		t.Fatalf("denied still wrote create=%d merge=%d", creates, merges)
	}
}

func TestDraftApplyRequiresProposalAndClient(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "ordinary", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.ApplySessionDraft(sid); !errors.Is(err, engine.ErrDraftCoreOff) {
		t.Fatalf("no client %v", err)
	}
	h.e.Core = &core.Fake{}
	if _, err := h.e.ApplySessionDraft(sid); !errors.Is(err, engine.ErrDraftNoProposal) {
		t.Fatalf("no proposal %v", err)
	}
}

func TestDraftRefusesAgentMembersAndMissingSourceTurn(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "describe", "")
	if err != nil {
		t.Fatal(err)
	}
	bad := interviewDraft(sid)
	bad.Members = append(bad.Members, engine.DraftMember{MemberKind: "agent", MemberID: "Interviewer", DefinitionJSON: `{"source_turn":"x"}`})
	r := completeDraft(t, h, h.claim(), bad)
	if r.Outcome != engine.OutcomeBlocked || r.Draft != nil {
		t.Fatalf("agent member %+v", r)
	}
	sid2, err := h.e.StartRun("test", "again", "")
	if err != nil {
		t.Fatal(err)
	}
	missing := interviewDraft(sid2)
	missing.Members[0].DefinitionJSON = `{"name":"Customer"}`
	r = completeDraft(t, h, h.claim(), missing)
	if r.Outcome != engine.OutcomeBlocked {
		t.Fatalf("missing source_turn %+v", r)
	}
}

func TestGuestCannotIngestDraftActions(t *testing.T) {
	if engine.AllowedTurnActionType(engine.ActionCoreDraft) || engine.AllowedTurnActionType(engine.ActionCoreDraftApply) {
		t.Fatal("guest ingest of draft actions")
	}
}

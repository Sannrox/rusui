package engine_test

import (
	"errors"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/store"
)

func TestHaltOneAgentSkipsQueuedAndStopsLeased(t *testing.T) {
	h := setup(t)
	alice, err := h.e.StartRunExec("test", "alice work", "", "", "", "", store.ExecutionSpace, "alice", "alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := h.e.StartRunExec("test", "bob work", "", "", "", "", store.ExecutionSpace, "bob", "bob")
	if err != nil {
		t.Fatal(err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatalf("claim %v %v", c, err)
	}
	turn, err := store.GetTurn(h.st, c.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turn.SessionID != alice {
		t.Fatalf("first claim session %d want alice %d", turn.SessionID, alice)
	}
	rep, err := h.e.ApplyFreeze("agent", "alice", store.HaltRecord{Reason: "freeze one agent", Operator: "op"})
	if err != nil || !rep.Halted {
		t.Fatalf("freeze %+v %v", rep, err)
	}
	if len(rep.Stopped) != 1 || rep.Stopped[0].TurnID != c.Job.ID {
		t.Fatalf("stopped %+v", rep.Stopped)
	}
	var aliceState string
	if err := h.st.DB.QueryRow(`SELECT state FROM turns WHERE session_id=?`, alice).Scan(&aliceState); err != nil {
		t.Fatal(err)
	}
	if aliceState != "queued" {
		t.Fatalf("alice after halt %s", aliceState)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err == nil {
		t.Fatal("complete after halt succeeded on halted generation")
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil {
		t.Fatalf("bob claim %v %v", c2, err)
	}
	bobTurn, err := store.GetTurn(h.st, c2.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bobTurn.SessionID != bob {
		t.Fatalf("second claim session %d want bob %d", bobTurn.SessionID, bob)
	}
	c3, err := h.e.Claim("example/test-repo")
	if err != nil {
		t.Fatal(err)
	}
	if c3 != nil {
		t.Fatalf("alice queued turn claimed while halted: job %d", c3.Job.ID)
	}
	if _, err := h.e.Complete(c2.Job.ID, c2.Job.LeaseGeneration, c2.Job.ClaimedRevision, runArt(c2)); err != nil {
		t.Fatal(err)
	}
	c3, err = h.e.Claim("example/test-repo")
	if err != nil {
		t.Fatal(err)
	}
	if c3 != nil {
		t.Fatalf("alice claimed after bob finished while still halted: job %d", c3.Job.ID)
	}
	if err := h.e.ResumeHalt("agent", "alice", store.HaltRecord{Operator: "op"}); err != nil {
		t.Fatal(err)
	}
	c4, err := h.e.Claim("example/test-repo")
	if err != nil || c4 == nil {
		t.Fatalf("resume claim %v %v", c4, err)
	}
	resumed, err := store.GetTurn(h.st, c4.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.SessionID != alice {
		t.Fatalf("resume session %d want alice %d", resumed.SessionID, alice)
	}
	events, err := store.ListHaltEvents(h.st)
	if err != nil || len(events) != 2 {
		t.Fatalf("events %d %v", len(events), err)
	}
	if events[0].Action != "halt" || events[0].Source != store.HaltSourcePlane || events[1].Action != "resume" {
		t.Fatalf("events %+v", events)
	}
}

func TestProjectHaltStopsReviewSessionsWithEmptyProject(t *testing.T) {
	h := setup(t)
	h.putRefresh(issue(1))
	c := h.claim()
	if c == nil {
		t.Fatal("claim")
	}
	rep, err := h.e.Halt("project", "test", store.HaltRecord{Reason: "stop reviews", Operator: "op"})
	if err != nil || !rep.Halted {
		t.Fatalf("halt %+v %v", rep, err)
	}
	if len(rep.Stopped) != 1 || rep.Stopped[0].TurnID != c.Job.ID {
		t.Fatalf("stopped %+v", rep.Stopped)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil {
		t.Fatal(err)
	}
	if c2 != nil {
		t.Fatalf("review claimed while project halted: job %d", c2.Job.ID)
	}
	if err := h.e.ResumeHalt("project", "test", store.HaltRecord{Operator: "op"}); err != nil {
		t.Fatal(err)
	}
	c3, err := h.e.Claim("example/test-repo")
	if err != nil || c3 == nil {
		t.Fatalf("resume claim %v %v", c3, err)
	}
	if c3.Job.ID != c.Job.ID {
		t.Fatalf("resume job %d want %d", c3.Job.ID, c.Job.ID)
	}
}

func TestStartRunRefusesHaltedAgent(t *testing.T) {
	h := setup(t)
	if _, err := h.e.Halt("agent", "alice", store.HaltRecord{Reason: "stop", Operator: "op"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.StartRunExec("test", "alice work", "", "", "", "", store.ExecutionSpace, "alice", "alice"); !errors.Is(err, engine.ErrHalted) {
		t.Fatalf("alice start %v", err)
	}
	if _, err := h.e.StartRunExec("test", "bob work", "", "", "", "", store.ExecutionSpace, "bob", "bob"); err != nil {
		t.Fatal(err)
	}
}

func TestStartRunHaltUsesCreatedActor(t *testing.T) {
	h := setup(t)
	if _, err := h.e.Halt("agent", "alice", store.HaltRecord{Reason: "stop", Operator: "op"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.StartRunExec("test", "alice work", "", "", "", "", store.ExecutionSpace, "", "alice"); !errors.Is(err, engine.ErrHalted) {
		t.Fatalf("createdBy alice start %v", err)
	}
}

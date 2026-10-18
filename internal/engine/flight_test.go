package engine_test

import (
	"errors"
	"testing"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/flight"
	"github.com/sannrox/rusui/internal/store"
)

func TestCompleteRecordsFlightAndReplay(t *testing.T) {
	h := setup(t)
	if _, err := h.e.StartRun("test", "summarize obj", ""); err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	h.e.NoteRead(c.Job.ID, flight.Object{ID: "obj-a", Revision: "rev-1", Readers: []string{"alice"}})
	body := `{"sessionUpdate":"tool_call","toolCallId":"t1","title":"read_file","kind":"read","status":"pending","rawInput":{"path":"a.txt"}}`
	turn, err := store.GetTurn(h.st, c.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	sid := turn.SessionID
	tid := c.Job.ID
	if err := store.InsertAction(h.st, store.Action{
		ID: "tool-1", SessionID: &sid, TurnID: &tid, Repo: c.Job.Repo, Item: c.Job.Item,
		Type: acp.ActionUpdate, ReasonCode: acp.ReasonRecorded,
		EvidenceClass: acp.EvidenceObserved, LimitSentence: acp.LimitSentence,
		Body: body,
	}); err != nil {
		t.Fatal(err)
	}
	effect := `{"sessionUpdate":"tool_call","toolCallId":"t2","title":"bash","kind":"execute","status":"pending","rawInput":{"command":"rm -rf /"}}`
	if err := store.InsertAction(h.st, store.Action{
		ID: "tool-2", SessionID: &sid, TurnID: &tid, Repo: c.Job.Repo, Item: c.Job.Item,
		Type: acp.ActionUpdate, ReasonCode: acp.ReasonRecorded,
		EvidenceClass: acp.EvidenceObserved, LimitSentence: acp.LimitSentence,
		Body: effect,
	}); err != nil {
		t.Fatal(err)
	}
	a := runArt(c)
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	rec, err := store.GetFlightRecord(h.st, c.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ReceiptID == "" || rec.Prompt != "summarize obj" {
		t.Fatalf("record %+v", rec)
	}
	if len(rec.Snapshot.Objects) != 1 || rec.Snapshot.Objects[0].ID != "obj-a" {
		t.Fatalf("snapshot %+v", rec.Snapshot)
	}
	sink := &flight.DenyEffects{}
	rep := flight.Replay(rec, rec.Snapshot, sink)
	if rep.Divergence != "" {
		t.Fatalf("divergence %q", rep.Divergence)
	}
	if len(rep.ToolInputs) != 2 || rep.ToolInputs[0].Name != "read_file" {
		t.Fatalf("inputs %+v", rep.ToolInputs)
	}
	if len(rep.Denied) != 1 || rep.Denied[0] != "bash" || len(sink.DeniedNames) != 1 {
		t.Fatalf("denied %+v sink %+v", rep.Denied, sink.DeniedNames)
	}
	live := rec.Snapshot
	live.Objects = append([]flight.Object(nil), live.Objects...)
	live.Objects[0].Revision = "rev-2"
	div := flight.Replay(rec, live, &flight.DenyEffects{})
	if div.Divergence != "object_revision" || div.ObjectID != "obj-a" {
		t.Fatalf("changed snapshot %+v", div)
	}
	view := flight.Open(rec, "mallory")
	if _, err := view.Object("obj-a"); !errors.Is(err, flight.ErrNoRead) {
		t.Fatalf("mallory %v", err)
	}
}

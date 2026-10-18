package flight

import (
	"errors"
	"testing"
)

func sampleRecord() Record {
	return Normalize(Record{
		SessionID: 1,
		TurnID:    9,
		ReceiptID: "9:1:1",
		Prompt:    "summarize obj",
		Model:     Model{Name: "test-model", Provider: "test"},
		Tools: []Tool{
			{ID: "t1", Name: "read_file", Input: map[string]any{"path": "a.txt"}, Result: "ok"},
			{ID: "t2", Name: "bash", Input: map[string]any{"command": "curl https://example.invalid"}},
		},
		Snapshot: Snapshot{Objects: []Object{
			{ID: "obj-a", Revision: "rev-1", Readers: []string{"alice"}},
			{ID: "obj-b", Revision: "rev-7", Readers: []string{"bob"}},
		}},
		Context: []string{"retrieved:obj-a"},
	})
}

type applySink struct {
	DenyEffects
	applied int
}

func (s *applySink) Apply(name string, input map[string]any) error {
	s.applied++
	return s.DenyEffects.Apply(name, input)
}

func TestReplaySameSnapshotKeepsToolInputs(t *testing.T) {
	rec := sampleRecord()
	sink := &applySink{}
	rep := Replay(rec, rec.Snapshot, sink)
	if rep.Divergence != "" {
		t.Fatalf("divergence %q", rep.Divergence)
	}
	want := []ToolInput{
		{ID: "t1", Name: "read_file", Input: rec.Tools[0].Input},
		{ID: "t2", Name: "bash", Input: rec.Tools[1].Input},
	}
	if !EqualInputs(rep.ToolInputs, want) {
		t.Fatalf("tool inputs %+v", rep.ToolInputs)
	}
	if len(rep.Denied) != 1 || rep.Denied[0] != "bash" {
		t.Fatalf("denied %+v", rep.Denied)
	}
	if sink.applied != 0 {
		t.Fatalf("applied %d", sink.applied)
	}
	if len(sink.DeniedNames) != 1 || sink.DeniedNames[0] != "bash" {
		t.Fatalf("sink denials %+v", sink.DeniedNames)
	}
}

func TestReplayReportsObjectRevisionDivergence(t *testing.T) {
	rec := sampleRecord()
	live := rec.Snapshot
	live.Objects = append([]Object(nil), live.Objects...)
	live.Objects[0].Revision = "rev-changed"
	sink := &applySink{}
	rep := Replay(rec, live, sink)
	if rep.Divergence != "object_revision" || rep.ObjectID != "obj-a" {
		t.Fatalf("report %+v", rep)
	}
	if rep.WantRevision != "rev-1" || rep.GotRevision != "rev-changed" {
		t.Fatalf("revisions %+v", rep)
	}
	if sink.applied != 0 {
		t.Fatalf("applied %d", sink.applied)
	}
}

func TestOpenHidesUnreadSnapshotPart(t *testing.T) {
	rec := sampleRecord()
	alice := Open(rec, "alice")
	if _, err := alice.Object("obj-b"); !errors.Is(err, ErrNoRead) {
		t.Fatalf("alice obj-b %v", err)
	}
	got, err := alice.Object("obj-a")
	if err != nil || got.Revision != "rev-1" {
		t.Fatalf("alice obj-a %+v %v", got, err)
	}
	for _, o := range alice.Snapshot.Objects {
		if o.ID == "obj-b" {
			t.Fatalf("alice view leaked obj-b")
		}
	}
	bob := Open(rec, "bob")
	if _, err := bob.Object("obj-a"); !errors.Is(err, ErrNoRead) {
		t.Fatalf("bob obj-a %v", err)
	}
}

func TestManifestIsSchemaV1(t *testing.T) {
	rec := sampleRecord()
	m := ManifestFrom(rec)
	if m.SchemaVersion != SchemaVersion || m.EvidenceDigest == "" || m.Bindings.InputsDigest != rec.Snapshot.Digest {
		t.Fatalf("manifest %+v", m)
	}
	if m.Bindings.InputsDigest != DigestSnapshot(rec.Snapshot.Objects) {
		t.Fatalf("inputs digest %s", m.Bindings.InputsDigest)
	}
}

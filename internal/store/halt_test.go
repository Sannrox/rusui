package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestHaltOverlaySkipsOtherAgents(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "halt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	rec := HaltRecord{Reason: "freeze", Operator: "op", Source: HaltSourcePlane}
	if err := st.Tx(func(tx *sql.Tx) error {
		return SetHaltTx(tx, HaltScopeAgent, "alice", rec)
	}); err != nil {
		t.Fatal(err)
	}
	var alice, bob bool
	if err := st.Tx(func(tx *sql.Tx) error {
		var err error
		alice, err = Halted(tx, DefaultTenantID, "test", "alice")
		if err != nil {
			return err
		}
		bob, err = Halted(tx, DefaultTenantID, "test", "bob")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !alice || bob {
		t.Fatalf("alice=%v bob=%v", alice, bob)
	}
	if err := st.Tx(func(tx *sql.Tx) error {
		return ClearHaltTx(tx, HaltScopeAgent, "alice", rec)
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Tx(func(tx *sql.Tx) error {
		alice, err = Halted(tx, DefaultTenantID, "test", "alice")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if alice {
		t.Fatal("alice still halted")
	}
	events, err := ListHaltEvents(st)
	if err != nil || len(events) != 2 {
		t.Fatalf("events %d %v", len(events), err)
	}
	if events[0].Action != "halt" || events[0].Source != HaltSourcePlane || events[1].Action != "resume" {
		t.Fatalf("events %+v", events)
	}
}

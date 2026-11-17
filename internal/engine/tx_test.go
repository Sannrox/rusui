package engine

import (
	"errors"
	"io"
	"log"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sannrox/rusui/internal/flight"
	"github.com/sannrox/rusui/internal/store"
)

func txTestEngine(t *testing.T) (*Engine, *[]string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "tx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var sent []string
	e := &Engine{Store: st, Log: log.New(io.Discard, "", 0)}
	e.Notify = func(msg string) { sent = append(sent, msg) }
	return e, &sent
}

// An exception raised inside a transaction is sent once after commit, and
// never for an attempt that rolls back.
func TestExceptionTxSendsOnlyAfterCommit(t *testing.T) {
	e, sent := txTestEngine(t)
	boom := errors.New("boom")
	if err := e.tx(func(tx *store.Tx) error {
		e.exceptionTx(tx, "rolled back")
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
	if len(*sent) != 0 {
		t.Fatalf("rolled-back attempt notified %v", *sent)
	}
	if err := e.tx(func(tx *store.Tx) error {
		e.exceptionTx(tx, "committed")
		if len(*sent) != 0 {
			t.Errorf("notified before commit %v", *sent)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*sent, []string{"committed"}) {
		t.Fatalf("sent %v", *sent)
	}
}

// Flight reads survive a rolled-back attempt and are dropped once a record
// that holds them commits; reads noted meanwhile stay.
func TestFlightReadsDropOnlyAfterCommit(t *testing.T) {
	e, _ := txTestEngine(t)
	e.NoteRead(7, flight.Object{ID: "a"})
	_ = e.tx(func(tx *store.Tx) error {
		if got := e.readsTx(tx, 7); len(got) != 1 {
			t.Errorf("reads %v", got)
		}
		return errors.New("abort")
	})
	if err := e.tx(func(tx *store.Tx) error {
		if got := e.readsTx(tx, 7); len(got) != 1 || got[0].ID != "a" {
			t.Errorf("reads after rollback %v", got)
		}
		e.NoteRead(7, flight.Object{ID: "b"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if left := e.flightReads[7]; len(left) != 1 || left[0].ID != "b" {
		t.Fatalf("reads after commit %v", left)
	}
}

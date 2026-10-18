package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/flight"
)

func TestFlightRecordAppendOnlyAndRetentionWipe(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if _, err := st.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1,'run','o/r',1,'issue','open',?)`, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	res, err := st.DB.Exec(`INSERT INTO turns (session_id, lane, state) VALUES (1,'run','completed')`)
	if err != nil {
		t.Fatal(err)
	}
	turnID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	rec := flight.Normalize(flight.Record{
		SessionID: 1, TurnID: turnID, ReceiptID: "1:1:1", Prompt: "secret-prompt",
		Tools: []flight.Tool{{ID: "t1", Name: "read_file", Input: map[string]any{"path": "a"}}},
		Snapshot: flight.Snapshot{Objects: []flight.Object{
			{ID: "obj-a", Revision: "r1", Readers: []string{"alice"}},
		}},
	})
	if err := InsertFlightRecord(st, rec, now); err != nil {
		t.Fatal(err)
	}
	if err := InsertFlightRecord(st, rec, now); !errors.Is(err, flight.ErrOverwrite) {
		t.Fatalf("same receipt %v", err)
	}
	follow := rec
	follow.ReceiptID = "1:2:2"
	follow.Prompt = "follow-up"
	if err := InsertFlightRecord(st, follow, now); err != nil {
		t.Fatal(err)
	}
	got, err := GetFlightRecord(st, turnID)
	if err != nil || got.Prompt != "follow-up" || got.Snapshot.Objects[0].ID != "obj-a" {
		t.Fatalf("got %+v %v", got, err)
	}
	if err := st.Tx(func(tx *sql.Tx) error { return ClearSessionDataTx(st, tx, 1) }); err != nil {
		t.Fatal(err)
	}
	if _, err := GetFlightRecord(st, turnID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("retained %v", err)
	}
}

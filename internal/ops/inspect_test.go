package ops

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

func TestInspectLiveDoesNotMigrateAndReadsPause(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rusui.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := st.Tx(func(tx *sql.Tx) error {
		if err := store.OverlaySet(tx, "pause:global", "1"); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1,'review','example/test-repo',1,'issue','open',?)`, now); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO turns (session_id, lane, state) VALUES (1, 'review', 'leased')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	schema, paused, live, err := InspectLive(path)
	if err != nil {
		t.Fatal(err)
	}
	if schema != store.CurrentSchema {
		t.Fatalf("schema %d", schema)
	}
	if !paused {
		t.Fatal("pause not read")
	}
	if len(live) != 1 || live[0].Repo != "example/test-repo" {
		t.Fatalf("live %+v", live)
	}
}

func TestInspectLiveMissingFileFails(t *testing.T) {
	if _, _, _, err := InspectLive(filepath.Join(t.TempDir(), "no.db")); err == nil {
		t.Fatal("expected error")
	}
}

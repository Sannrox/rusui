package engine_test

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/sessionexport"
	"github.com/sannrox/rusui/internal/store"
)

func sessionExportKey(t *testing.T) []byte {
	t.Helper()
	key, err := sessionexport.ParseKey(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func admitSource(t *testing.T, h *harn, source string) {
	t.Helper()
	raw := strings.Replace(fixture, "projects:\n", "session_export_sources: ["+source+"]\nprojects:\n", 1)
	p, err := policy.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(p)
}

func TestImportSessionMintsNewIDWithoutLease(t *testing.T) {
	h := setup(t)
	h.e.SessionExportKey = sessionExportKey(t)
	planeID, err := store.PlaneID(h.st)
	if err != nil {
		t.Fatal(err)
	}
	admitSource(t, h, planeID)
	sid, err := h.e.StartRun("test", "secret prompt body", "")
	if err != nil {
		t.Fatal(err)
	}
	now := h.clk.T.UTC().Format(time.RFC3339Nano)
	if _, err := h.st.DB.Exec(`UPDATE turns SET state='leased', lease_generation=3, lease_expires_at=? WHERE session_id=?`, now, sid); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GrantSessionAccess(h.st, sid, "other", "default"); err != nil {
		t.Fatal(err)
	}
	raw, err := h.e.ExportSession(sid, sessionexport.DefaultTTL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret prompt body") {
		t.Fatal("prompt body exported")
	}
	got, err := h.e.ImportSession("test", raw)
	if err != nil {
		t.Fatal(err)
	}
	if got == 0 || got == sid {
		t.Fatalf("imported id %d source %d", got, sid)
	}
	imported, err := store.GetSession(h.st, got)
	if err != nil {
		t.Fatal(err)
	}
	if imported.Prompt != "" {
		t.Fatalf("prompt restored %q", imported.Prompt)
	}
	if imported.GuestSessionID != "" {
		t.Fatalf("guest handle restored %q", imported.GuestSessionID)
	}
	var turns, leased int
	if err := h.st.DB.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN state='leased' OR lease_generation>0 THEN 1 ELSE 0 END),0) FROM turns WHERE session_id=?`, got).Scan(&turns, &leased); err != nil {
		t.Fatal(err)
	}
	if turns != 0 || leased != 0 {
		t.Fatalf("turns=%d leased=%d", turns, leased)
	}
	granted, err := store.SessionGranted(h.st, got, "other")
	if err != nil {
		t.Fatal(err)
	}
	if granted {
		t.Fatal("restored a grant")
	}
}

func TestImportSessionRefusesUnadmittedSource(t *testing.T) {
	h := setup(t)
	h.e.SessionExportKey = sessionExportKey(t)
	sid, err := h.e.StartRun("test", "hello", "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := h.e.ExportSession(sid, sessionexport.DefaultTTL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.ImportSession("test", raw); !errors.Is(err, sessionexport.ErrSource) {
		t.Fatalf("default off %v", err)
	}
}

func TestImportSessionOverlayCannotAddSource(t *testing.T) {
	h := setup(t)
	h.e.SessionExportKey = sessionExportKey(t)
	planeID, err := store.PlaneID(h.st)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := h.e.StartRun("test", "hello", "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := h.e.ExportSession(sid, sessionexport.DefaultTTL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.st.Tx(func(tx *sql.Tx) error {
		return store.OverlaySet(tx, sessionexport.OverlayKey(planeID), "1")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.ImportSession("test", raw); !errors.Is(err, sessionexport.ErrSource) {
		t.Fatalf("overlay add %v", err)
	}
	admitSource(t, h, planeID)
	if err := h.st.Tx(func(tx *sql.Tx) error {
		return store.OverlaySet(tx, sessionexport.OverlayKey(planeID), sessionexport.OverlayOff)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.ImportSession("test", raw); !errors.Is(err, sessionexport.ErrSource) {
		t.Fatalf("overlay remove %v", err)
	}
	other := hex.EncodeToString([]byte("not-in-yaml-plane-id-bytes!!"))
	if err := h.st.Tx(func(tx *sql.Tx) error {
		return store.OverlaySet(tx, sessionexport.OverlayKey(other), "1")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.ImportSession("test", raw); !errors.Is(err, sessionexport.ErrSource) {
		t.Fatalf("overlay still cannot add %v", err)
	}
}

func TestExportSessionRefusesMissingKey(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "hello", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.ExportSession(sid, 0, nil); !errors.Is(err, sessionexport.ErrNoKey) {
		t.Fatalf("export %v", err)
	}
	if _, err := h.e.ImportSession("test", []byte(`{}`)); !errors.Is(err, sessionexport.ErrNoKey) {
		t.Fatalf("import %v", err)
	}
}

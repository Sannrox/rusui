package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSupportedRunnerTopologyIsOneLocalHost(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "runner.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var n int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM runners`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("runners %d", n)
	}
	var name string
	if err := st.DB.QueryRow(`SELECT name FROM runners`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != LocalRunnerName {
		t.Fatalf("name %q", name)
	}
	at := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	if err := TouchRunner(st, "other-host", at); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM runners`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("hello must not mint a second runner, have %d", n)
	}
	if err := TouchRunner(st, LocalRunnerName, at); err != nil {
		t.Fatal(err)
	}
	var seen string
	if err := st.DB.QueryRow(`SELECT last_seen_at FROM runners WHERE name=?`, LocalRunnerName).Scan(&seen); err != nil {
		t.Fatal(err)
	}
	if seen != at.Format(time.RFC3339Nano) {
		t.Fatalf("last_seen %q", seen)
	}
}

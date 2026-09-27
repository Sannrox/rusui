package store

import (
	"path/filepath"
	"testing"
)

func TestMeasurementUnknownStaysNull(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	rows, err := st.DB.Query(`PRAGMA table_info(turn_measurements)`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def *string
		if err := rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			t.Fatal(err)
		}
		seen[name] = true
	}
	for _, col := range []string{"prompt", "body", "diff", "tool_argument", "credential", "terminal"} {
		if seen[col] {
			t.Fatalf("measurement column %s", col)
		}
	}
	for _, col := range []string{"turn_id", "session_id", "provider", "provider_version", "terminal_state", "duration_ms", "first_event_ms", "wake_ms", "resume", "permission_decision", "tokens_in", "tokens_out", "publication"} {
		if !seen[col] {
			t.Fatalf("missing %s", col)
		}
	}
}

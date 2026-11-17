package store

import (
	"testing"
)

func TestMeasurementUnknownStaysNull(t *testing.T) {
	st := openTestStore(t)
	seen := tableColumns(t, st, "turn_measurements")
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

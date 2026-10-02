package acp

import (
	"encoding/json"
	"testing"
)

// Every shell command of the Claude guest reaches the permission gate
// (#442): the spawn carries an ask rule for Bash as one argument.
func TestClaudeSpawnAsksBeforeEveryShellCommand(t *testing.T) {
	argv, err := SpawnArgsFor(GuestClaude)
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range argv {
		if a == "--settings" && i+1 < len(argv) {
			var s struct {
				Permissions struct {
					Ask []string `json:"ask"`
				} `json:"permissions"`
			}
			if err := json.Unmarshal([]byte(argv[i+1]), &s); err != nil {
				t.Fatalf("settings %q: %v", argv[i+1], err)
			}
			if len(s.Permissions.Ask) != 1 || s.Permissions.Ask[0] != "Bash" {
				t.Fatalf("ask %v", s.Permissions.Ask)
			}
			return
		}
	}
	t.Fatalf("no --settings in %q", argv)
}

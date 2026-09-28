package ops

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestADR0030ScheduleNewSessionIsAccepted(t *testing.T) {
	root := filepath.Join("..", "..")
	adr := readRepoFile(t, root, "docs", "decisions", "0030-schedule-new-session.md")
	if !strings.Contains(adr, "- Status: Accepted") {
		t.Fatal("ADR 0030 must be Accepted")
	}
	for _, want := range []string{
		"A schedule fire creates a new",
		"It does not wake, prompt, or attach to",
		"[ADR 0006](0006-session-start.md) is not amended.",
		"Continuation is follow-up",
		"No implementation follow-up is authorized.",
	} {
		if !strings.Contains(adr, want) {
			t.Fatalf("ADR 0030 missing %q", want)
		}
	}
	idx := readRepoFile(t, root, "docs", "decisions", "README.md")
	if !strings.Contains(idx, "[0030](0030-schedule-new-session.md) | A schedule fire starts a new session | Accepted") {
		t.Fatal("ADR index missing 0030")
	}
	old := readRepoFile(t, root, "docs", "decisions", "0006-session-start.md")
	if !strings.Contains(old, "0030-schedule-new-session.md") {
		t.Fatal("ADR 0006 must link forward to 0030")
	}
	glossary := readRepoFile(t, root, "CONTEXT.md")
	if !strings.Contains(glossary, "Continuation of an existing session is follow-up") {
		t.Fatal("CONTEXT.md Schedule must record follow-up as continuation")
	}
}

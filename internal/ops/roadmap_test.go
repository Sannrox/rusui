package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishedRoadmapFollowsAcceptedSequence(t *testing.T) {
	root := filepath.Join("..", "..")
	adr, err := os.ReadFile(filepath.Join(root, "docs", "decisions", "0010-hybrid-roadmap-sequence.md"))
	if err != nil {
		t.Fatal(err)
	}
	head := string(adr[:min(len(adr), 400)])
	if !strings.Contains(head, "- Status: Accepted") {
		t.Fatalf("ADR 0010 not Accepted:\n%s", head)
	}
	rm, err := os.ReadFile(filepath.Join(root, "ROADMAP.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(rm)
	if strings.Contains(text, "is **Proposed**") {
		t.Fatal("ROADMAP still treats ADR 0010 as Proposed")
	}
	for _, want := range []string{"M1 Dependable sessions", "M2 Verified delivery", "M6 Stable core", "94aabd7"} {
		if !strings.Contains(text, want) {
			t.Fatalf("ROADMAP missing %q", want)
		}
	}
	if strings.Contains(text, "live GitHub writes") && !strings.Contains(text, "does not enable live GitHub writes") {
		t.Fatal("ROADMAP must not authorize live GitHub writes")
	}
}

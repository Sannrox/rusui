package ops

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestADR0031CLICommandTreeIsAccepted(t *testing.T) {
	root := filepath.Join("..", "..")
	adr := readRepoFile(t, root, "docs", "decisions", "0031-cli-command-tree.md")
	if !strings.Contains(adr, "- Status: Accepted") {
		t.Fatal("ADR 0031 must be Accepted")
	}
	for _, want := range []string{
		"No-arg still starts the plane",
		"Verbs stay flat",
		"rusui logs SESSION_ID` stays action receipts",
		"rusui envlog SESSION_ID",
		"No implementation follow-up is authorized except the `envlog` name",
	} {
		if !strings.Contains(adr, want) {
			t.Fatalf("ADR 0031 missing %q", want)
		}
	}
	idx := readRepoFile(t, root, "docs", "decisions", "README.md")
	if !strings.Contains(idx, "[0031](0031-cli-command-tree.md) | The rusui CLI stays a flat verb list | Accepted") {
		t.Fatal("ADR index missing 0031")
	}
	old := readRepoFile(t, root, "docs", "decisions", "0003-operator-surface.md")
	if !strings.Contains(old, "0031-cli-command-tree.md") {
		t.Fatal("ADR 0003 must link forward to 0031")
	}
}

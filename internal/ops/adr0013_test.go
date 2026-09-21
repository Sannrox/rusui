package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readRepoFile(t *testing.T, root string, parts ...string) string {
	t.Helper()
	p := filepath.Join(append([]string{root}, parts...)...)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestADR0013PublicationContractIsAccepted(t *testing.T) {
	root := filepath.Join("..", "..")
	adr := readRepoFile(t, root, "docs", "decisions", "0013-publication-authority.md")
	if !strings.Contains(adr, "- Status: Accepted") {
		t.Fatal("ADR 0013 must be Accepted")
	}
	if strings.Contains(adr, "- Status: Proposed") {
		t.Fatal("ADR 0013 still Proposed")
	}
	for _, want := range []string{"source", "task", "candidate", "proof", "publication", "open_pr", "update_pr", "denied", "proven"} {
		if !strings.Contains(adr, want) {
			t.Fatalf("ADR 0013 missing %q", want)
		}
	}

	arch := readRepoFile(t, root, "ARCHITECTURE.md")
	for _, want := range []string{"open_pr", "update_pr", "proven"} {
		if !strings.Contains(arch, want) {
			t.Fatalf("ARCHITECTURE.md missing publication contract %q", want)
		}
	}
	if strings.Contains(arch, "No live GitHub mutation, no implement, no land.") {
		t.Fatal("ARCHITECTURE.md current contract still forbids all GitHub mutation")
	}
	if !strings.Contains(arch, "Human merge") {
		t.Fatal("ARCHITECTURE.md must keep human merge as the land path")
	}

	idx := readRepoFile(t, root, "docs", "decisions", "README.md")
	if !strings.Contains(idx, "0013-publication-authority.md") {
		t.Fatal("ADR index missing 0013")
	}
	if !strings.Contains(idx, "Exact-artifact verification and plane-owned publication | Accepted") {
		t.Fatal("ADR index must mark 0013 Accepted")
	}

	glossary := readRepoFile(t, root, "CONTEXT.md")
	for _, want := range []string{"**Source**:", "**Candidate**:", "**Proof**:", "**Publication**:"} {
		if !strings.Contains(glossary, want) {
			t.Fatalf("CONTEXT.md missing noun %q", want)
		}
	}
}

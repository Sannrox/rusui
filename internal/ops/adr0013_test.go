package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestADR0013PublicationGateIsProposed(t *testing.T) {
	root := filepath.Join("..", "..")
	adr, err := os.ReadFile(filepath.Join(root, "docs", "decisions", "0013-publication-authority.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(adr)
	if !strings.Contains(text, "- Status: Proposed") {
		t.Fatal("ADR 0013 must stay Proposed until acceptance rewrites the contract")
	}
	if !strings.Contains(text, "does not enable live GitHub writes") {
		t.Fatal("ADR 0013 must not authorize live GitHub writes")
	}
	for _, want := range []string{"source", "task", "candidate", "proof", "publication", "open_pr", "update_pr", "denied"} {
		if !strings.Contains(text, want) {
			t.Fatalf("ADR 0013 missing %q", want)
		}
	}
	idx, err := os.ReadFile(filepath.Join(root, "docs", "decisions", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(idx), "0013-publication-authority.md") {
		t.Fatal("ADR index missing 0013")
	}
}

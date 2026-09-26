package ops

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestADR0023P8PilotNarrowContract(t *testing.T) {
	root := filepath.Join("..", "..")
	adr := readRepoFile(t, root, "docs", "decisions", "0023-p8-pilot-narrow.md")
	if !strings.Contains(adr, "- Status: Accepted") {
		t.Fatal("ADR 0023 must be Accepted")
	}
	for _, want := range []string{"narrow", "#103", "PR 284", "stale source"} {
		if !strings.Contains(adr, want) {
			t.Fatalf("ADR 0023 missing %q", want)
		}
	}
	old := readRepoFile(t, root, "docs", "decisions", "0014-pilot-evaluation-deferred.md")
	if !strings.Contains(old, "Superseded by [ADR 0023]") {
		t.Fatal("ADR 0014 must link forward to ADR 0023")
	}
	idx := readRepoFile(t, root, "docs", "decisions", "README.md")
	if !strings.Contains(idx, "[0023](0023-p8-pilot-narrow.md) | P8 ten-task live pilot is narrow | Accepted") {
		t.Fatal("ADR index missing 0023")
	}
	results := readRepoFile(t, root, "docs", "proofs", "p8-pilot-results.md")
	if !strings.Contains(results, "P8-10") || !strings.Contains(results, "409") {
		t.Fatal("p8-pilot-results.md must record P8-10 stale admit")
	}
}

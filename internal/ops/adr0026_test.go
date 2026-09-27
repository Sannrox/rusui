package ops

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestADR0026HarnessModelUpstreamIsAccepted(t *testing.T) {
	root := filepath.Join("..", "..")
	adr := readRepoFile(t, root, "docs", "decisions", "0026-harness-model-upstream.md")
	if !strings.Contains(adr, "- Status: Accepted") {
		t.Fatal("ADR 0026 must be Accepted")
	}
	for _, want := range []string{
		"guest selects the harness and the proxy protocol.",
		"ANTHROPIC_DEFAULT_OPUS_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"It does not contain a compiled model id.",
		"A ready catalog is not proof the guest can prompt.",
		"Model keys stay on the plane.",
	} {
		if !strings.Contains(adr, want) {
			t.Fatalf("ADR 0026 missing %q", want)
		}
	}
	idx := readRepoFile(t, root, "docs", "decisions", "README.md")
	if !strings.Contains(idx, "[0026](0026-harness-model-upstream.md) | Harness, model, and upstream are independent | Accepted") {
		t.Fatal("ADR index missing 0026")
	}
	old := readRepoFile(t, root, "docs", "decisions", "0017-claude-guest-and-model-upstream.md")
	if !strings.Contains(old, "0026-harness-model-upstream.md") {
		t.Fatal("ADR 0017 must link forward to 0026")
	}
}

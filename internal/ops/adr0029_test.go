package ops

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestADR0029SingleHostRunnerIsAccepted(t *testing.T) {
	root := filepath.Join("..", "..")
	adr := readRepoFile(t, root, "docs", "decisions", "0029-single-host-runner.md")
	if !strings.Contains(adr, "- Status: Accepted") {
		t.Fatal("ADR 0029 must be Accepted")
	}
	for _, want := range []string{
		"The supported topology is one `rusui-runner` on",
		"`TouchRunner` updates `last_seen_at` for that name.",
		"not insert a second runner.",
		"Adopting a fleet is a refused action",
		"No implementation follow-up is authorized.",
	} {
		if !strings.Contains(adr, want) {
			t.Fatalf("ADR 0029 missing %q", want)
		}
	}
	idx := readRepoFile(t, root, "docs", "decisions", "README.md")
	if !strings.Contains(idx, "[0029](0029-single-host-runner.md) | The supported runner topology remains one host | Accepted") {
		t.Fatal("ADR index missing 0029")
	}
	for _, name := range []string{
		"0007-environment-snapshot.md",
		"0011-unattended-session-contract.md",
		"0028-container-isolation-profile.md",
	} {
		body := readRepoFile(t, root, "docs", "decisions", name)
		if !strings.Contains(body, "0029-single-host-runner.md") {
			t.Fatalf("%s must link forward to 0029", name)
		}
	}
	arch := readRepoFile(t, root, "ARCHITECTURE.md")
	if !strings.Contains(arch, "0029-single-host-runner.md") {
		t.Fatal("ARCHITECTURE must name ADR 0029")
	}
	glossary := readRepoFile(t, root, "CONTEXT.md")
	if !strings.Contains(glossary, "single `runners` row named `local`") {
		t.Fatal("CONTEXT.md Runner must name the single local row")
	}
}

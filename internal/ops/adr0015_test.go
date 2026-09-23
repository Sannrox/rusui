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

func TestADR0015AgentPublicationContractIsAccepted(t *testing.T) {
	root := filepath.Join("..", "..")
	adr := readRepoFile(t, root, "docs", "decisions", "0015-agent-publication.md")
	if !strings.Contains(adr, "- Status: Accepted") {
		t.Fatal("ADR 0015 must be Accepted")
	}
	for _, want := range []string{"Supersedes: [ADR 0013]", "gh pr create", "implement", "Co-authored-by: rusui <noreply@rusui.invalid>", "Rusui-Session:", "Humans merge"} {
		if !strings.Contains(adr, want) {
			t.Fatalf("ADR 0015 missing %q", want)
		}
	}

	old := readRepoFile(t, root, "docs", "decisions", "0013-publication-authority.md")
	if !strings.Contains(old, "- Status: Superseded by [`0015-agent-publication.md`]") {
		t.Fatal("ADR 0013 must link forward to ADR 0015")
	}

	arch := readRepoFile(t, root, "ARCHITECTURE.md")
	for _, want := range []string{"## Publication (ADR 0015)", "Rusui-Session", "Human merge"} {
		if !strings.Contains(arch, want) {
			t.Fatalf("ARCHITECTURE.md missing publication contract %q", want)
		}
	}
	for _, stale := range []string{"## Publication (ADR 0013)", "Model CLIs never\nreceive GitHub write tokens."} {
		if strings.Contains(arch, stale) {
			t.Fatalf("ARCHITECTURE.md still states superseded contract %q", stale)
		}
	}

	idx := readRepoFile(t, root, "docs", "decisions", "README.md")
	for _, want := range []string{
		"Exact-artifact verification and plane-owned publication | Superseded by 0015",
		"[0015](0015-agent-publication.md) | The agent publishes its own pull requests | Accepted",
	} {
		if !strings.Contains(idx, want) {
			t.Fatalf("ADR index missing %q", want)
		}
	}

	glossary := readRepoFile(t, root, "CONTEXT.md")
	if !strings.Contains(glossary, "**Publication**:\nA pull request the agent opens") {
		t.Fatal("CONTEXT.md Publication noun must describe agent-driven publication")
	}
}

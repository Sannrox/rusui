package ops

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestADR0024SessionSurfaceIsAccepted(t *testing.T) {
	root := filepath.Join("..", "..")
	adr := readRepoFile(t, root, "docs", "decisions", "0024-session-surface.md")
	if !strings.Contains(adr, "- Status: Accepted") {
		t.Fatal("ADR 0024 must be Accepted")
	}
	for _, want := range []string{
		"The `rusui` CLI and the embedded console are the surfaces that open a\nsession.",
		"its transcript, its recorded tool-call\nevents, and its current diff",
		"Where the session runs is a field on the\nsession.",
		"Closing the client leaves the session running.",
		"The terminal does not stand in for the transcript.",
		"An ACP\neditor remains another client of the same session.",
		"Slack remains notify and\napprove.",
		"Sumika remains the supervisor of an experimental local process.",
		"It is not the session list, and it\nis not how a managed session is read.",
		"This ADR does not rewrite [ARCHITECTURE.md](../../ARCHITECTURE.md).",
		"[#282](https://github.com/Sannrox/rusui/issues/282) is the follow-up that\nmay rewrite that contract",
		"It cannot define the session UI, and it cannot require Sumika\n  attach and the managed console to behave as one operator surface.",
	} {
		if !strings.Contains(adr, want) {
			t.Fatalf("ADR 0024 missing %q", want)
		}
	}

	idx := readRepoFile(t, root, "docs", "decisions", "README.md")
	const indexRow = "[0024](0024-session-surface.md) | The CLI and the console open a session | Accepted"
	if !strings.Contains(idx, indexRow) {
		t.Fatal("ADR index missing 0024")
	}

	forward := map[string]string{
		"0003-operator-surface.md":          "0024-session-surface.md",
		"0016-local-interactive-runtime.md": "0024-session-surface.md",
		"0019-rusui-attach-client.md":       "0024-session-surface.md",
	}
	for name, link := range forward {
		body := readRepoFile(t, root, "docs", "decisions", name)
		if !strings.Contains(body, link) {
			t.Fatalf("%s must link forward to %s", name, link)
		}
	}

	arch := readRepoFile(t, root, "ARCHITECTURE.md")
	if strings.Contains(arch, "ADR 0024") || strings.Contains(arch, "0024-session-surface") {
		t.Fatal("ARCHITECTURE.md must stay unchanged by ADR 0024")
	}

	rel := regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)
	for _, m := range rel.FindAllStringSubmatch(adr, -1) {
		target := m[1]
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "#") {
			continue
		}
		target = strings.SplitN(target, "#", 2)[0]
		path := filepath.Join(root, "docs", "decisions", target)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("ADR 0024 link %q does not resolve: %v", m[1], err)
		}
	}
}

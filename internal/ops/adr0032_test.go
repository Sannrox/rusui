package ops

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestADR0032NamedServicesIsAccepted(t *testing.T) {
	root := filepath.Join("..", "..")
	adr := readRepoFile(t, root, "docs", "decisions", "0032-named-services.md")
	if !strings.Contains(adr, "- Status: Accepted") {
		t.Fatal("ADR 0032 must be Accepted")
	}
	for _, want := range []string{
		"Two new fields, and unknown fields fail closed",
		"The plane injects the port and identity, not a public origin",
		"A preview grant names a service",
		"The raw-port mint stays, for the operator only",
		"This ADR authorizes implementation of D1 to D5 as later work",
	} {
		if !strings.Contains(adr, want) {
			t.Fatalf("ADR 0032 missing %q", want)
		}
	}
	idx := readRepoFile(t, root, "docs", "decisions", "README.md")
	if !strings.Contains(idx, "[0032](0032-named-services.md) | Named services declare a port and a health path | Accepted") {
		t.Fatal("ADR index missing 0032")
	}
	for _, name := range []string{"0004-rusui-services-yaml.md", "0012-operator-access.md"} {
		if !strings.Contains(readRepoFile(t, root, "docs", "decisions", name), "0032-named-services.md") {
			t.Fatalf("%s must link forward to 0032", name)
		}
	}
}

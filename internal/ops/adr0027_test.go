package ops

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestADR0027GuestReachabilityAskIsAccepted(t *testing.T) {
	root := filepath.Join("..", "..")
	adr := readRepoFile(t, root, "docs", "decisions", "0027-guest-reachability-ask.md")
	if !strings.Contains(adr, "- Status: Accepted") {
		t.Fatal("ADR 0027 must be Accepted")
	}
	for _, want := range []string{
		"`policy.yaml` is the grant.",
		"Do not pin the experimental descriptor.",
		"A missing descriptor keeps that implicit ask.",
		"an ask wider than the grant refuses",
		"Unattended work does not prompt.",
		"use a rusui type namespace.",
		"No implementation follow-up is authorized until the descriptor format",
	} {
		if !strings.Contains(adr, want) {
			t.Fatalf("ADR 0027 missing %q", want)
		}
	}
	idx := readRepoFile(t, root, "docs", "decisions", "README.md")
	if !strings.Contains(idx, "[0027](0027-guest-reachability-ask.md) | The guest image does not declare reachability yet | Accepted") {
		t.Fatal("ADR index missing 0027")
	}
	for _, name := range []string{
		"0008-p1-isolation-split.md",
		"0009-credential-broker.md",
		"0022-public-repo-isolation.md",
	} {
		body := readRepoFile(t, root, "docs", "decisions", name)
		if !strings.Contains(body, "0027-guest-reachability-ask.md") {
			t.Fatalf("%s must link forward to 0027", name)
		}
	}
	arch := readRepoFile(t, root, "ARCHITECTURE.md")
	if !strings.Contains(arch, "0027-guest-reachability-ask.md") {
		t.Fatal("ARCHITECTURE must name ADR 0027")
	}
}

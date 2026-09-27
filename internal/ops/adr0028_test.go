package ops

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/env"
)

func TestADR0028ContainerIsolationProfileIsAccepted(t *testing.T) {
	root := filepath.Join("..", "..")
	adr := readRepoFile(t, root, "docs", "decisions", "0028-container-isolation-profile.md")
	if !strings.Contains(adr, "- Status: Accepted") {
		t.Fatal("ADR 0028 must be Accepted")
	}
	for _, want := range []string{
		"The supported IsolationProfile for unattended sessions",
		"is **container**",
		"A snapshot is the reusable workspace tree keyed by `source_hash`.",
		"Adopting a stronger profile is a refused action",
		"No implementation follow-up is authorized.",
		"`Container.Kind()` is `container`",
	} {
		if !strings.Contains(adr, want) {
			t.Fatalf("ADR 0028 missing %q", want)
		}
	}
	idx := readRepoFile(t, root, "docs", "decisions", "README.md")
	if !strings.Contains(idx, "[0028](0028-container-isolation-profile.md) | The supported machine isolation profile remains the container | Accepted") {
		t.Fatal("ADR index missing 0028")
	}
	for _, name := range []string{
		"0007-environment-snapshot.md",
		"0008-p1-isolation-split.md",
		"0022-public-repo-isolation.md",
		"0027-guest-reachability-ask.md",
	} {
		body := readRepoFile(t, root, "docs", "decisions", name)
		if !strings.Contains(body, "0028-container-isolation-profile.md") {
			t.Fatalf("%s must link forward to 0028", name)
		}
	}
	arch := readRepoFile(t, root, "ARCHITECTURE.md")
	if !strings.Contains(arch, "0028-container-isolation-profile.md") {
		t.Fatal("ARCHITECTURE must name ADR 0028")
	}
	glossary := readRepoFile(t, root, "CONTEXT.md")
	if !strings.Contains(glossary, "a stronger runtime is not selected") {
		t.Fatal("CONTEXT.md Machine isolation must record that a stronger runtime is not selected")
	}
}

func TestDiagnoseStillFailsClosedWithoutContainerRuntime(t *testing.T) {
	dir := t.TempDir()
	pol := filepath.Join(dir, "policy.yaml")
	if err := copyExamplePolicy(t, pol); err != nil {
		t.Fatal(err)
	}
	rep := Diagnose(Options{
		PolicyPath:  pol,
		LookRuntime: func() (env.Runtime, error) { return nil, fmt.Errorf("none") },
		Env:         func(string) string { return "" },
	})
	if rep.Ready {
		t.Fatal("diagnose must not be ready without a container runtime")
	}
	var runtime Check
	for _, c := range rep.Checks {
		if c.Name == "runtime" {
			runtime = c
			break
		}
	}
	if runtime.Status != StatusUnavailable || !runtime.Blocker {
		t.Fatalf("runtime check %+v", runtime)
	}
}

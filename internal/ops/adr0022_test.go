package ops

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/env"
)

func TestADR0022PublicRepoIsolationContractIsAccepted(t *testing.T) {
	root := filepath.Join("..", "..")
	adr := readRepoFile(t, root, "docs", "decisions", "0022-public-repo-isolation.md")
	if !strings.Contains(adr, "- Status: Accepted") {
		t.Fatal("ADR 0022 must be Accepted")
	}
	for _, want := range []string{
		"container",
		"visibility: public",
		"Fail closed",
		"process driver",
		"rusui diagnose",
	} {
		if !strings.Contains(adr, want) {
			t.Fatalf("ADR 0022 missing %q", want)
		}
	}

	arch := readRepoFile(t, root, "ARCHITECTURE.md")
	for _, want := range []string{
		"Public-repository unattended sessions use the **container** driver",
		"ADR 0022",
		"fail closed when the\ncontainer runtime is missing",
	} {
		if !strings.Contains(arch, want) {
			t.Fatalf("ARCHITECTURE.md missing isolation contract %q", want)
		}
	}
	if strings.Contains(arch, "v1 is **trusted local execution**") {
		t.Fatal("ARCHITECTURE.md still states trusted-local as the v1 default")
	}

	idx := readRepoFile(t, root, "docs", "decisions", "README.md")
	if !strings.Contains(idx, "[0022](0022-public-repo-isolation.md) | Public-repository unattended sessions default to the container driver | Accepted") {
		t.Fatal("ADR index missing 0022")
	}

	old := readRepoFile(t, root, "docs", "decisions", "0008-p1-isolation-split.md")
	if !strings.Contains(old, "0022-public-repo-isolation.md") {
		t.Fatal("ADR 0008 must link forward to ADR 0022")
	}

	glossary := readRepoFile(t, root, "CONTEXT.md")
	if !strings.Contains(glossary, "The default for public-repository sessions is a container") {
		t.Fatal("CONTEXT.md Machine isolation must name the public-repository container default")
	}
}

func TestDiagnoseFailsClosedWithoutContainerRuntime(t *testing.T) {
	dir := t.TempDir()
	pol := filepath.Join(dir, "policy.yaml")
	if err := copyExamplePolicy(t, pol); err != nil {
		t.Fatal(err)
	}
	rep := Diagnose(Options{
		PolicyPath:  pol,
		LookRuntime: func() (env.Runtime, error) { return nil, fmt.Errorf("none") },
		Env: func(string) string {
			return ""
		},
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

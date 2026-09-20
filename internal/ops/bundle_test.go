package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/store"
)

func TestWriteBundleRedactsSecretsAndRecordsIdentity(t *testing.T) {
	dir := t.TempDir()
	secret := "super-secret-value"
	b := Bundle{
		Identity: ArtifactIdentity{
			Binary: "rusui", Commit: "abc1234", Schema: store.CurrentSchema, Topology: Topology,
			GuestImage: "rusui-guest:test",
		},
		Diagnose:   Report{Topology: Topology, Ready: false},
		Exclusions: []string{"private repo names in this fixture: none"},
	}
	if err := WriteBundle(dir, b, []string{secret, "also-in-exclusion-" + secret}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "diagnostics.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatal("secret leaked")
	}
	if !strings.Contains(string(raw), `"schema"`) || !strings.Contains(string(raw), Topology) {
		t.Fatalf("%s", raw)
	}
	ex, err := os.ReadFile(filepath.Join(dir, "exclusions.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ex), secret) {
		t.Fatal("exclusion leaked secret")
	}
}

func TestRedactLeavesShortValues(t *testing.T) {
	if Redact("x=ab", []string{"ab"}) != "x=ab" {
		t.Fatal("short secret should not redact")
	}
	if Redact("tok=abcdef", []string{"abcdef"}) != "tok=[redacted]" {
		t.Fatal(Redact("tok=abcdef", []string{"abcdef"}))
	}
}

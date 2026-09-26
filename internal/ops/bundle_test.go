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

func TestWriteBundleRedactsOperatorTokenAndModelUpstreamUserinfo(t *testing.T) {
	dir := t.TempDir()
	operator := "operator-token-value"
	userinfo := "gateway-user:gateway-pass"
	upstream := "https://" + userinfo + "@example.invalid/v1"
	getenv := func(k string) string {
		switch k {
		case "RUSUI_OPERATOR_TOKEN":
			return operator
		case "RUSUI_MODEL_UPSTREAM":
			return upstream
		default:
			return ""
		}
	}
	b := Bundle{
		Identity: ArtifactIdentity{Binary: "rusui", Commit: "abc1234", Schema: store.CurrentSchema, Topology: Topology},
		Diagnose: Report{
			Topology: Topology,
			Checks: []Check{{
				Name:   "model_upstream",
				Detail: "origin " + upstream + " token " + operator,
			}},
		},
	}
	if err := WriteBundle(dir, b, SecretValues(getenv)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "diagnostics.json"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, secret := range []string{operator, userinfo, "gateway-pass"} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret %q leaked: %s", secret, got)
		}
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

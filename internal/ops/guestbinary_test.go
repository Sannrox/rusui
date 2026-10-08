package ops

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sannrox/rusui/internal/env"
)

func TestGuestBinaryProbeUsesProjectDefaultAndCleansUp(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
		err    error
		status string
	}{
		{"available", "shikigami 2.0.0\n", nil, StatusReady},
		{"missing", "", errors.New("binary not found"), StatusUnavailable},
		{"empty response", "", nil, StatusUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "policy.yaml")
			raw := `version: 2
projects:
  first:
    guests: {default: shikigami, allowed: [shikigami]}
  second:
    guests: {default: shikigami, allowed: [shikigami]}
`
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			rt := &env.FakeRuntime{ExecHook: func(_ string, _ []string) error { return tc.err }, OutputHook: func(_ string, _ []string) []byte { return []byte(tc.output) }}
			checks := CheckGuestBinaries(rt, path, func(key string) string {
				switch key {
				case "RUSUI_GUEST":
					return "claude"
				case "RUSUI_GUEST_IMAGE":
					return "reference:fixture"
				default:
					return ""
				}
			})
			if len(checks) != 1 || checks[0].Name != "guest_shikigami" || checks[0].Status != tc.status || !checks[0].Blocker {
				t.Fatalf("probe checks: %+v", checks)
			}
			if len(rt.Created) != 1 || rt.Created[0].Image != "reference:fixture" || rt.Created[0].Network != "none" {
				t.Fatalf("probe containers: %+v", rt.Created)
			}
			if len(rt.Execs) != 1 || !slices.Equal(rt.Execs[0][1:], []string{"shikigami", "--version"}) {
				t.Fatalf("probe argv: %v", rt.Execs)
			}
			if len(rt.Removed) != 1 {
				t.Fatalf("probe container leaked: %v", rt.Removed)
			}
		})
	}
}

package policy

import (
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/guest"
	"gopkg.in/yaml.v3"
)

func TestGuestRegistryAndProjectChoices(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*File)
		bad    bool
	}{
		{"four existing guests", func(*File) {}, false},
		{"unknown guest", func(f *File) { f.Guests["extra"] = f.Guests["grok"] }, true},
		{"missing guest", func(f *File) { delete(f.Guests, "codex") }, true},
		{"native guest as ACP", func(f *File) { g := f.Guests["claude"]; g.Protocol = "acp"; f.Guests["claude"] = g }, true},
		{"unsupported Claude pin", func(f *File) { g := f.Guests["claude"]; g.Pin = "unsupported"; f.Guests["claude"] = g }, true},
		{"missing argv", func(f *File) { g := f.Guests["shikigami"]; g.Argv = nil; f.Guests["shikigami"] = g }, true},
		{"unlisted default", func(f *File) { p := f.Projects["test"]; p.Guests.Default = "codex"; f.Projects["test"] = p }, true},
		{"unknown allowed", func(f *File) {
			p := f.Projects["test"]
			p.Guests.Allowed = append(p.Guests.Allowed, "extra")
			f.Projects["test"] = p
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := File{Version: 2, Guests: guest.Builtin(), Projects: map[string]ProjectYAML{"test": {Guests: &ProjectGuests{Default: "shikigami", Allowed: []string{"shikigami", "claude"}}}}}
			tc.change(&f)
			raw, err := yaml.Marshal(f)
			if err != nil {
				t.Fatal(err)
			}
			p, err := Parse(raw)
			if tc.bad {
				if err == nil {
					t.Fatal("invalid guest policy accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Guests) != 4 || p.Projects["test"].Guests.Default != "shikigami" {
				t.Fatalf("guests %+v", p.Guests)
			}
		})
	}
	if _, err := Parse([]byte("version: 2\nprojects:\n  test:\n    guests:\n      default: grok\n      allowed: [grok]\n      unknown: true\n")); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown field: %v", err)
	}
}

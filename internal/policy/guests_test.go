package policy

import (
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/guest"
	"gopkg.in/yaml.v3"
)

var testImageDigest = "sha256:" + strings.Repeat("a", 64)

func secondACP(image string) guest.Entry {
	return guest.Entry{Protocol: guest.ProtocolACP, Argv: []string{"second-agent", "acp"}, Probe: []string{"second-agent", "--version"}, Pin: "2", Image: image}
}

func TestGuestRegistryAndProjectChoices(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*File)
		bad    bool
	}{
		{"four existing guests", func(*File) {}, false},
		{"unknown guest", func(f *File) { f.Guests["extra"] = f.Guests["grok"] }, true},
		{"qualified generic ACP", func(f *File) {
			entry := secondACP("example/second@" + testImageDigest)
			entry.Conformance, _ = guest.ConformanceDigest(entry)
			f.Guests["second"] = entry
			f.Projects["test"].Guests.Allowed = append(f.Projects["test"].Guests.Allowed, "second")
		}, false},
		{"unqualified generic ACP", func(f *File) {
			f.Guests["second"] = secondACP("example/second@" + testImageDigest)
		}, true},
		{"generic ACP digest from another image", func(f *File) {
			entry := secondACP("example/second@" + testImageDigest)
			entry.Conformance, _ = guest.ConformanceDigest(entry)
			entry.Image = "example/second@sha256:" + strings.Repeat("b", 64)
			f.Guests["second"] = entry
		}, true},
		{"generic ACP image pinned by tag", func(f *File) {
			entry := secondACP("example/second:2")
			pinned := entry
			pinned.Image = "example/second@" + testImageDigest
			entry.Conformance, _ = guest.ConformanceDigest(pinned)
			f.Guests["second"] = entry
		}, true},
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
			if (!tc.bad && tc.name == "qualified generic ACP" && len(p.Guests) != 5) || (tc.name != "qualified generic ACP" && len(p.Guests) != 4) || p.Projects["test"].Guests.Default != "shikigami" {
				t.Fatalf("guests %+v", p.Guests)
			}
		})
	}
	if _, err := Parse([]byte("version: 2\nprojects:\n  test:\n    guests:\n      default: grok\n      allowed: [grok]\n      unknown: true\n")); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown field: %v", err)
	}
}

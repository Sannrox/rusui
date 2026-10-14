package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/guest"
	"gopkg.in/yaml.v3"
)

var testImageDigest = "sha256:" + strings.Repeat("a", 64)

func secondACP(image string) guest.Entry {
	return guest.Entry{Protocol: guest.ProtocolACP, Argv: []string{"second-agent", "acp"}, Probe: []string{"second-agent", "--version"}, Pin: "2", Image: image}
}

var testConformanceKey = []byte(strings.Repeat("k", 32))

// signedACP returns a generic ACP entry with a receipt signed by key.
func signedACP(t *testing.T, key []byte, image string) guest.Entry {
	t.Helper()
	entry := secondACP(image)
	receipt, err := guest.SignReceipt(key, entry, time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	entry.Receipt = &receipt
	return entry
}

func TestGuestRegistryAndProjectChoices(t *testing.T) {
	pinned := "example/second@" + testImageDigest
	for _, tc := range []struct {
		name   string
		change func(*File)
		bad    bool
	}{
		{"four existing guests", func(*File) {}, false},
		{"unknown guest", func(f *File) { f.Guests["extra"] = f.Guests["grok"] }, true},
		{"qualified generic ACP", func(f *File) {
			f.Guests["second"] = signedACP(t, testConformanceKey, pinned)
			f.Projects["test"].Guests.Allowed = append(f.Projects["test"].Guests.Allowed, "second")
		}, false},
		{"unqualified generic ACP", func(f *File) {
			f.Guests["second"] = secondACP(pinned)
		}, true},
		{"generic ACP receipt from another image", func(f *File) {
			entry := signedACP(t, testConformanceKey, pinned)
			entry.Image = "example/second@sha256:" + strings.Repeat("b", 64)
			f.Guests["second"] = entry
		}, true},
		{"generic ACP receipt after an argv edit", func(f *File) {
			entry := signedACP(t, testConformanceKey, pinned)
			entry.Argv = []string{"other-agent", "acp"}
			f.Guests["second"] = entry
		}, true},
		{"generic ACP image pinned by tag", func(f *File) {
			entry := signedACP(t, testConformanceKey, pinned)
			entry.Image = "example/second:2"
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

func TestVerifyConformanceNeedsThePlaneSignature(t *testing.T) {
	pinned := "example/second@" + testImageDigest
	parse := func(entry guest.Entry) *Effective {
		t.Helper()
		f := File{Version: 2, Guests: guest.Builtin(), Projects: map[string]ProjectYAML{"test": {}}}
		f.Guests["second"] = entry
		raw, err := yaml.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		p, err := Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if err := parse(signedACP(t, testConformanceKey, pinned)).VerifyConformance(testConformanceKey); err != nil {
		t.Fatalf("valid receipt refused: %v", err)
	}
	// A receipt computed offline without the plane key binds the entry but
	// carries the wrong signature.
	forged := parse(signedACP(t, []byte(strings.Repeat("x", 32)), pinned))
	if err := forged.VerifyConformance(testConformanceKey); err == nil {
		t.Fatal("receipt signed without the plane key accepted")
	}
	if err := parse(signedACP(t, testConformanceKey, pinned)).VerifyConformance(nil); err == nil {
		t.Fatal("receipt accepted by a plane without a conformance key")
	}
}

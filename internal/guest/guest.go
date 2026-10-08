package guest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	KindGrok            = "grok"
	KindClaude          = "claude"
	KindCodex           = "codex"
	KindShikigami       = "shikigami"
	GrokACPVersion      = 1
	ClaudeCodeVersion   = "2.1.283"
	ClaudeStreamProto   = "stream-json"
	CodexAppServerProto = "app-server-2026-04-15"

	ProtocolACP    = "acp"
	ProtocolClaude = "claude-stream-json"
	ProtocolCodex  = "codex-app-server"
)

// Entry is the process and protocol pinned for a session. Empty Image uses
// the operator's existing environment image or the process driver.
type Entry struct {
	Protocol string   `yaml:"protocol" json:"protocol"`
	Argv     []string `yaml:"argv" json:"argv"`
	Probe    []string `yaml:"probe" json:"probe"`
	Pin      string   `yaml:"pin" json:"pin"`
	Image    string   `yaml:"image" json:"image"`
	// Conformance binds a generic ACP entry to the exact descriptor and
	// conformance suite that qualified it. Built-ins retain their legacy pins.
	Conformance string `yaml:"conformance,omitempty" json:"conformance,omitempty"`
}

const ConformanceSuite = "acp-subset-v1"

func ConformanceDigest(e Entry) string {
	e.Conformance = ""
	b, _ := json.Marshal(struct {
		Protocol string   `json:"protocol"`
		Argv     []string `json:"argv"`
		Probe    []string `json:"probe"`
		Pin      string   `json:"pin"`
		Image    string   `json:"image"`
	}{e.Protocol, e.Argv, e.Probe, e.Pin, e.Image})
	h := sha256.Sum256(append([]byte(ConformanceSuite+"\x00"), b...))
	return hex.EncodeToString(h[:])
}

// Builtin returns fresh registry data for legacy policies.
func Builtin() map[string]Entry {
	return map[string]Entry{
		KindGrok:      {Protocol: ProtocolACP, Argv: []string{"agent", "--permission-mode", "default", "agent", "stdio"}, Probe: []string{"agent", "--version"}, Pin: fmt.Sprintf("acp:%d", GrokACPVersion)},
		KindClaude:    {Protocol: ProtocolClaude, Argv: []string{"claude", "--print", "--input-format", ClaudeStreamProto, "--output-format", ClaudeStreamProto, "--verbose", "--permission-mode", "default", "--permission-prompt-tool", "stdio", "--settings", `{"permissions":{"ask":["Bash"]}}`}, Probe: []string{"claude", "--version"}, Pin: ClaudeCodeVersion},
		KindCodex:     {Protocol: ProtocolCodex, Argv: []string{"codex", "app-server", "--listen", "stdio://"}, Probe: []string{"codex", "--version"}, Pin: CodexAppServerProto},
		KindShikigami: {Protocol: ProtocolACP, Argv: []string{"shikigami", "--state", "./state", "acp"}, Probe: []string{"shikigami", "--version"}, Pin: "acp"},
	}
}

// DefaultShikigamiModel is the HTTP adapter's auto model in the pinned guest.
const DefaultShikigamiModel = "gpt-4.1-mini"

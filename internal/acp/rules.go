package acp

import (
	"encoding/json"
	"strings"
)

// Rule is one policy allow-rule (ADR 0005). Empty fields are wildcards.
type Rule struct {
	Tool    string `json:"tool"`
	Kind    string `json:"kind"`
	Command string `json:"command"`
}

// RulesGate allows a request when a rule matches the tool call.
type RulesGate struct {
	Rules []Rule
}

func (g RulesGate) Decide(p PermissionParams) Decision {
	if len(g.Rules) == 0 {
		return Decision{Matched: false, Allow: false}
	}
	title, kind, cmd := toolCallFields(p.ToolCall)
	for _, r := range g.Rules {
		if r.Tool != "" && !fieldMatch(r.Tool, title) && !fieldMatch(r.Tool, kind) {
			continue
		}
		if r.Kind != "" && !fieldMatch(r.Kind, kind) && !fieldMatch(r.Kind, title) {
			continue
		}
		if r.Command != "" && !strings.Contains(strings.ToLower(cmd+" "+title), strings.ToLower(r.Command)) {
			continue
		}
		return Decision{Matched: true, Allow: true}
	}
	return Decision{Matched: false, Allow: false}
}

func fieldMatch(want, got string) bool {
	if want == "" || got == "" {
		return false
	}
	return strings.EqualFold(want, got) || strings.Contains(strings.ToLower(got), strings.ToLower(want))
}

func toolCallFields(raw json.RawMessage) (title, kind, cmd string) {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return "", "", string(raw)
	}
	title, _ = m["title"].(string)
	if title == "" {
		title, _ = m["toolName"].(string)
	}
	kind, _ = m["kind"].(string)
	cmd, _ = m["command"].(string)
	if cmd == "" {
		if t, ok := m["title"].(string); ok {
			cmd = t
		}
	}
	return title, kind, cmd
}

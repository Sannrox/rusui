package acp

import (
	"encoding/json"
	"strings"
)

// Rule is one policy permission rule (ADR 0005). Empty fields are
// wildcards. Action is "allow" (the default) or "reject" (ADR 0017 D3).
type Rule struct {
	Tool    string `json:"tool"`
	Kind    string `json:"kind"`
	Command string `json:"command"`
	Action  string `json:"action,omitempty"`
}

const RuleReject = "reject"

// RulesGate answers a request from policy rules: any matching reject rule
// rejects it, otherwise a matching allow rule allows it.
type RulesGate struct {
	Rules []Rule
}

func (g RulesGate) Decide(p PermissionParams) Decision {
	if len(g.Rules) == 0 {
		return Decision{Matched: false, Allow: false}
	}
	title, kind, cmd := toolCallFields(p.ToolCall)
	for _, r := range g.Rules {
		if r.Action == RuleReject && ruleMatches(r, title, kind, cmd) {
			return Decision{Matched: true, Allow: false}
		}
	}
	for _, r := range g.Rules {
		if r.Action != RuleReject && ruleMatches(r, title, kind, cmd) {
			return Decision{Matched: true, Allow: true}
		}
	}
	return Decision{Matched: false, Allow: false}
}

func ruleMatches(r Rule, title, kind, cmd string) bool {
	if r.Tool == "" && r.Kind == "" && r.Command == "" {
		return r.Action != RuleReject // an empty reject rule would reject everything
	}
	if r.Tool != "" && !fieldMatch(r.Tool, title) && !fieldMatch(r.Tool, kind) {
		return false
	}
	if r.Kind != "" && !fieldMatch(r.Kind, kind) && !fieldMatch(r.Kind, title) {
		return false
	}
	if r.Command != "" && !strings.Contains(strings.ToLower(cmd+" "+title), strings.ToLower(r.Command)) {
		return false
	}
	return true
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

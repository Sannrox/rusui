package acp

import (
	"encoding/json"
	"path"
	"slices"
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
// rejects it, otherwise a matching allow rule allows it. A shell line is
// allowed by command rules only if every command in it is (#473).
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
	if g.allows(title, kind, cmd) {
		return Decision{Matched: true, Allow: true}
	}
	return Decision{Matched: false, Allow: false}
}

// allows reports whether an allow rule without a command matches, or
// every command in the lexed line begins with the words of some allow rule
// whose tool and kind match.
func (g RulesGate) allows(title, kind, cmd string) bool {
	var prefixes [][]string
	for _, r := range g.Rules {
		if r.Action == RuleReject || !fieldsMatch(r, title, kind) {
			continue
		}
		if r.Command == "" {
			return true
		}
		if words := strings.Fields(strings.ToLower(r.Command)); len(words) > 0 {
			prefixes = append(prefixes, words)
		}
	}
	return allowCommand(prefixes, lexCommands(strings.ToLower(cmd)))
}

func ruleMatches(r Rule, title, kind, cmd string) bool {
	if r.Tool == "" && r.Kind == "" && r.Command == "" {
		return r.Action != RuleReject // an empty reject rule would reject everything
	}
	if !fieldsMatch(r, title, kind) {
		return false
	}
	if r.Command == "" {
		return true
	}
	// A reject matches any command in the line, and the title too when it
	// differs from the command.
	words := strings.Fields(strings.ToLower(r.Command))
	return rejectCommand(words, lexCommands(strings.ToLower(cmd))) ||
		(title != cmd && rejectCommand(words, lexCommands(strings.ToLower(title))))
}

func fieldsMatch(r Rule, title, kind string) bool {
	if r.Tool != "" && !fieldMatch(r.Tool, title) && !fieldMatch(r.Tool, kind) {
		return false
	}
	return r.Kind == "" || fieldMatch(r.Kind, kind) || fieldMatch(r.Kind, title)
}

// allowCommand reports whether segs is non-empty and every command in it
// begins with one of prefixes. A command that redirects output (a word
// containing '>') never matches.
func allowCommand(prefixes, segs [][]string) bool {
	if len(segs) == 0 {
		return false
	}
	for _, seg := range segs {
		if slices.ContainsFunc(seg, func(w string) bool { return strings.Contains(w, ">") }) {
			return false
		}
		if !slices.ContainsFunc(prefixes, func(p []string) bool { return len(seg) >= len(p) && equalPrefix(seg, p) }) {
			return false
		}
	}
	return true
}

// rejectCommand reports whether any command in segs has words[0] (or a
// path ending in it) followed, in order, by the remaining words, so
// wrappers (`env`, `FOO=1`) and interleaved flags (`npm --tag x publish`)
// still match.
func rejectCommand(words []string, segs [][]string) bool {
	if len(words) == 0 {
		return false
	}
	for _, seg := range segs {
		for i, w := range seg {
			if path.Base(w) == words[0] && inOrder(words[1:], seg[i+1:]) {
				return true
			}
		}
	}
	return false
}

func inOrder(want, got []string) bool {
	for _, w := range got {
		if len(want) == 0 {
			break
		}
		if w == want[0] {
			want = want[1:]
		}
	}
	return len(want) == 0
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

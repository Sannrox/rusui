package acp

import (
	"encoding/json"
	"testing"
)

func TestRulesGateMatchAndMiss(t *testing.T) {
	g := RulesGate{Rules: []Rule{{Tool: "shell", Kind: "execute"}}}
	raw := json.RawMessage(`{"title":"shell","kind":"execute","command":"git status"}`)
	d := g.Decide(PermissionParams{ToolCall: raw})
	if !d.Matched || !d.Allow {
		t.Fatalf("%+v", d)
	}
	d = g.Decide(PermissionParams{ToolCall: json.RawMessage(`{"title":"fetch","kind":"read"}`)})
	if d.Matched || d.Allow {
		t.Fatalf("miss %+v", d)
	}
	cmd := RulesGate{Rules: []Rule{{Command: "git"}}}
	d = cmd.Decide(PermissionParams{ToolCall: json.RawMessage(`{"title":"git status","kind":"execute"}`)})
	if !d.Matched {
		t.Fatal("command wildcard")
	}
	empty := RulesGate{}
	d = empty.Decide(PermissionParams{ToolCall: raw})
	if d.Matched {
		t.Fatal("empty rules")
	}
}

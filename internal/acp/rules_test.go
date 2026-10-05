package acp

import (
	"encoding/json"
	"strings"
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

func TestCommandRulesMatchLexedSegments(t *testing.T) {
	g := RulesGate{Rules: []Rule{
		{Kind: "execute", Command: "ls"},
		{Command: "npm"},
		{Command: "npm publish", Action: RuleReject},
	}}
	ask := func(cmd string) Decision {
		raw, _ := json.Marshal(map[string]string{"title": "Bash", "kind": "execute", "command": cmd})
		return g.Decide(PermissionParams{ToolCall: raw})
	}
	for _, cmd := range []string{
		"ls", "ls -la", "LS -la", "ls; ls -a", "ls | ls", "ls && npm test", "npm test",
		"ls $(ls)", "npm run lint -- --fix",
	} {
		if d := ask(cmd); !d.Matched || !d.Allow {
			t.Errorf("not allowed: %q %+v", cmd, d)
		}
	}
	for _, cmd := range []string{
		"ls; rm -rf .git", "ls && rm -rf .git", "ls || rm x", "ls | sh", "ls & rm x",
		"ls\nrm x", "ls $(rm x)", "ls `rm x`", `ls "$(rm x)"`, "ls > .git/config",
		"lsblk", "rm -rf .git # ls", "FOO=1 ls", "/tmp/ls", "", "(rm x)",
	} {
		if d := ask(cmd); d.Allow {
			t.Errorf("allowed: %q %+v", cmd, d)
		}
	}
	for _, cmd := range []string{
		"npm publish", "npm  publish", "'npm' publish", `"npm" "publish"`, `n\pm publish`,
		"npm --tag x publish", "/usr/bin/npm publish", "env FOO=1 npm publish",
		"npm test && npm publish", "ls $(npm publish)", "NPM PUBLISH",
	} {
		if d := ask(cmd); !d.Matched || d.Allow {
			t.Errorf("not rejected: %q %+v", cmd, d)
		}
	}
	if d := ask(`git commit -m "npm publish"`); d.Matched {
		t.Errorf("quoted argument matched a rule: %+v", d)
	}
}

func TestCommandRuleWordPrefix(t *testing.T) {
	for _, tc := range []struct {
		rule, line    string
		allow, reject bool
	}{
		{"git status", "git status --short", true, true},
		{"git status", "git  'status'", true, true},
		{"git status", "git stash", false, false},
		{"git status", "git", false, false},
		{"go test", "go test ./... | tee log", false, true},
		{"go test", "go vet && go test ./...", false, true},
		{"go test", "go -C dir test", false, true},
		{"make", "make validate", true, true},
		{"make", "cmake .", false, false},
	} {
		segs := lexCommands(tc.line)
		if got := allowCommand([][]string{strings.Fields(tc.rule)}, segs); got != tc.allow {
			t.Errorf("allow %q on %q = %v", tc.rule, tc.line, got)
		}
		if got := rejectCommand(strings.Fields(tc.rule), segs); got != tc.reject {
			t.Errorf("reject %q on %q = %v", tc.rule, tc.line, got)
		}
	}
}

package acp

import (
	"errors"
	"testing"
)

func TestRetentionWriteGateDeniesIndefiniteWriteWithoutOverride(t *testing.T) {
	next := RulesGate{Rules: []Rule{{Tool: "shell"}}}
	g := RetentionWriteGate{Next: next, Class: "indefinite"}
	d := g.Decide(PermissionParams{ToolCall: []byte(`{"title":"shell","kind":"execute"}`)})
	if !d.Matched || d.Allow {
		t.Fatalf("write %+v", d)
	}
	read := g.Decide(PermissionParams{ToolCall: []byte(`{"title":"fetch","kind":"read"}`)})
	if read.Allow {
		t.Fatalf("read falls through unmatched %+v", read)
	}
}

func TestRetentionWriteGateOverrideAllowsWrite(t *testing.T) {
	g := RetentionWriteGate{
		Next:     RulesGate{Rules: []Rule{{Tool: "shell"}}},
		Class:    "indefinite",
		Override: true,
	}
	d := g.Decide(PermissionParams{ToolCall: []byte(`{"title":"shell","kind":"execute"}`)})
	if !d.Matched || !d.Allow {
		t.Fatalf("%+v", d)
	}
}

func TestRetentionWriteGateShortFallsThrough(t *testing.T) {
	g := RetentionWriteGate{Next: RulesGate{Rules: []Rule{{Tool: "shell"}}}}
	d := g.Decide(PermissionParams{ToolCall: []byte(`{"title":"shell","kind":"execute"}`)})
	if !d.Matched || !d.Allow {
		t.Fatalf("%+v", d)
	}
}

func TestRetentionWriteGateLiveOverride(t *testing.T) {
	override := false
	g := RetentionWriteGate{
		Next:  RulesGate{Rules: []Rule{{Tool: "shell"}}},
		Class: "indefinite",
		Live:  func() (string, bool, error) { return "indefinite", override, nil },
	}
	write := PermissionParams{ToolCall: []byte(`{"title":"shell","kind":"execute"}`)}
	if d := g.Decide(write); !d.Matched || d.Allow {
		t.Fatalf("live deny %+v", d)
	}
	override = true
	if d := g.Decide(write); !d.Matched || !d.Allow {
		t.Fatalf("live allow %+v", d)
	}
}

func TestRetentionWriteGateLiveErrorDeniesWrite(t *testing.T) {
	g := RetentionWriteGate{
		Next:  RulesGate{Rules: []Rule{{Tool: "shell"}}},
		Class: "short",
		Live:  func() (string, bool, error) { return "", false, errors.New("live") },
	}
	d := g.Decide(PermissionParams{ToolCall: []byte(`{"title":"shell","kind":"execute"}`)})
	if !d.Matched || d.Allow {
		t.Fatalf("%+v", d)
	}
}

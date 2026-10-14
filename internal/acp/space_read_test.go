package acp

import (
	"errors"
	"testing"
)

func TestSpaceReadGateDeniesWriteUntilGranted(t *testing.T) {
	next := RulesGate{Rules: []Rule{{Tool: "shell"}}}
	g := SpaceReadGate{Next: next}
	write := PermissionParams{ToolCall: []byte(`{"title":"shell","kind":"execute"}`)}
	if d := g.Decide(write); !d.Matched || d.Allow {
		t.Fatalf("ungranted write %+v", d)
	}
	read := PermissionParams{ToolCall: []byte(`{"title":"fetch","kind":"read"}`)}
	if d := g.Decide(read); d.Matched {
		t.Fatalf("read should fall through unmatched %+v", d)
	}
	granted := SpaceReadGate{Next: next, Grants: []string{"execute"}}
	if d := granted.Decide(write); !d.Matched || !d.Allow {
		t.Fatalf("granted write %+v", d)
	}
	all := SpaceReadGate{Next: next, Grants: []string{"write"}}
	if d := all.Decide(write); !d.Matched || !d.Allow {
		t.Fatalf("write class %+v", d)
	}
}

func TestSpaceReadGateLiveOverridesStaleGrants(t *testing.T) {
	next := RulesGate{Rules: []Rule{{Tool: "shell"}}}
	write := PermissionParams{ToolCall: []byte(`{"title":"shell","kind":"execute"}`)}
	grants := []string{"write"}
	g := SpaceReadGate{Next: next, Grants: []string{"write"}, Live: func() ([]string, error) { return grants, nil }}
	if d := g.Decide(write); !d.Matched || !d.Allow {
		t.Fatalf("live write %+v", d)
	}
	grants = nil
	if d := g.Decide(write); !d.Matched || d.Allow {
		t.Fatalf("revoked live %+v", d)
	}
	fail := SpaceReadGate{Next: next, Grants: []string{"write"}, Live: func() ([]string, error) { return nil, errors.New("live") }}
	if d := fail.Decide(write); !d.Matched || d.Allow {
		t.Fatalf("failed live %+v", d)
	}
}

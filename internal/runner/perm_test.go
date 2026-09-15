package runner

import (
	"testing"

	"github.com/sannrox/rusui/internal/acp"
)

func TestPermissionGateEmptyIsDeny(t *testing.T) {
	g := permissionGate(&Assignment{})
	d := g.Decide(acp.PermissionParams{ToolCall: []byte(`{"title":"shell"}`)})
	if d.Matched || d.Allow {
		t.Fatalf("%+v", d)
	}
}

func TestPermissionGateUsesRules(t *testing.T) {
	g := permissionGate(&Assignment{Permissions: []acp.Rule{{Tool: "shell"}}})
	d := g.Decide(acp.PermissionParams{ToolCall: []byte(`{"title":"shell","kind":"execute"}`)})
	if !d.Matched || !d.Allow {
		t.Fatalf("%+v", d)
	}
}

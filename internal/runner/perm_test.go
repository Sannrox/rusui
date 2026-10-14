package runner

import (
	"testing"

	"github.com/sannrox/rusui/internal/acp"
)

func TestPermissionGateEmptyIsDeny(t *testing.T) {
	g := permissionGate(&Assignment{ExecutionMode: "user"})
	d := g.Decide(acp.PermissionParams{ToolCall: []byte(`{"title":"shell"}`)})
	if d.Matched || d.Allow {
		t.Fatalf("%+v", d)
	}
}

func TestPermissionGateUsesRules(t *testing.T) {
	g := permissionGate(&Assignment{Permissions: []acp.Rule{{Tool: "shell"}}, ToolGrants: []string{"write"}})
	d := g.Decide(acp.PermissionParams{ToolCall: []byte(`{"title":"shell","kind":"execute"}`)})
	if !d.Matched || !d.Allow {
		t.Fatalf("%+v", d)
	}
}

func TestPermissionGateSpaceDeniesWriteWithoutGrant(t *testing.T) {
	g := permissionGate(&Assignment{Permissions: []acp.Rule{{Tool: "shell"}}, ExecutionMode: "space"})
	d := g.Decide(acp.PermissionParams{ToolCall: []byte(`{"title":"shell","kind":"execute"}`)})
	if !d.Matched || d.Allow {
		t.Fatalf("%+v", d)
	}
	read := g.Decide(acp.PermissionParams{ToolCall: []byte(`{"title":"fetch","kind":"read"}`)})
	if read.Allow {
		t.Fatalf("read %+v", read)
	}
}

func TestPermissionGateLiveToolsOverrideClaimedGrants(t *testing.T) {
	grants := []string{"write"}
	g := permissionGate(&Assignment{
		Permissions:   []acp.Rule{{Tool: "shell"}},
		ExecutionMode: "space",
		ToolGrants:    []string{"write"},
		LiveTools:     func() ([]string, error) { return grants, nil },
	})
	write := acp.PermissionParams{ToolCall: []byte(`{"title":"shell","kind":"execute"}`)}
	if d := g.Decide(write); !d.Matched || !d.Allow {
		t.Fatalf("live grant %+v", d)
	}
	grants = nil
	if d := g.Decide(write); !d.Matched || d.Allow {
		t.Fatalf("live revoke %+v", d)
	}
}

func TestPermissionGateIndefiniteDeniesWriteWithoutOverride(t *testing.T) {
	g := permissionGate(&Assignment{
		Permissions:   []acp.Rule{{Tool: "shell"}},
		ExecutionMode: "space",
		ToolGrants:    []string{"write"},
		Retention:     "indefinite",
	})
	d := g.Decide(acp.PermissionParams{ToolCall: []byte(`{"title":"shell","kind":"execute"}`)})
	if !d.Matched || d.Allow {
		t.Fatalf("indefinite write %+v", d)
	}
	read := g.Decide(acp.PermissionParams{ToolCall: []byte(`{"title":"fetch","kind":"read"}`)})
	if read.Allow {
		t.Fatalf("indefinite read %+v", read)
	}
}

func TestPermissionGateIndefiniteOverrideAllowsWrite(t *testing.T) {
	g := permissionGate(&Assignment{
		Permissions:   []acp.Rule{{Tool: "shell"}},
		ExecutionMode: "space",
		ToolGrants:    []string{"write"},
		Retention:     "indefinite",
		WriteOverride: true,
	})
	d := g.Decide(acp.PermissionParams{ToolCall: []byte(`{"title":"shell","kind":"execute"}`)})
	if !d.Matched || !d.Allow {
		t.Fatalf("override %+v", d)
	}
}

package runner

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/acp"
)

// permissionAnswer runs one real ACP client/agent exchange in which the
// agent asks to run cmd, and returns the option the runner's gate chose.
func permissionAnswer(t *testing.T, a *Assignment, cmd string) string {
	t.Helper()
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	raw, _ := json.Marshal(map[string]string{"toolCallId": "tc-1", "title": "run_terminal_command", "command": cmd})
	answered := make(chan string, 1)
	agent := &acp.FakeAgent{In: agentIn, Out: agentOut, ToolCall: raw, PermissionOption: func(o string) { answered <- o }}
	go func() { _ = agent.Run() }()
	t.Cleanup(func() { _ = clientIn.Close(); _ = clientOut.Close(); _ = agentIn.Close(); _ = agentOut.Close() })
	c := &acp.Client{In: clientIn, Out: clientOut, Perm: permissionGate(a),
		Wait: func(context.Context, acp.PermissionParams) acp.Decision { return acp.Decision{} }}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sid, err := c.SessionNew(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SessionPrompt(ctx, sid, "go"); err != nil {
		t.Fatal(err)
	}
	select {
	case o := <-answered:
		return o
	case <-ctx.Done():
		t.Fatal("no permission answer")
	}
	return ""
}

func TestImplementSessionFenceOverridesPolicyAllow(t *testing.T) {
	allowAll := []acp.Rule{{Command: "gh"}, {Command: "git"}}
	implement := &Assignment{GitHubToken: "tok", Permissions: allowAll}
	if got := permissionAnswer(t, implement, "gh pr merge 7 --squash"); got != "reject-once" {
		t.Fatalf("implement merge answered %q", got)
	}
	if got := permissionAnswer(t, implement, "gh pr create --fill"); got != "allow-once" {
		t.Fatalf("implement create answered %q", got)
	}
	ordinary := &Assignment{Permissions: allowAll}
	if got := permissionAnswer(t, ordinary, "gh pr merge 7 --squash"); got != "allow-once" {
		t.Fatalf("non-implement session should follow policy, answered %q", got)
	}
}

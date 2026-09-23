package acp

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveClaudeConformance runs the ADR 0002 conformance set against the
// pinned Claude Code adapter (ADR 0017 D1). It needs claude-agent-acp on
// PATH and model access in the environment (e.g. ANTHROPIC_BASE_URL and
// ANTHROPIC_AUTH_TOKEN pointing at a plane model proxy or CLI proxy).
func TestLiveClaudeConformance(t *testing.T) {
	if os.Getenv("RUSUI_ACP_LIVE_CLAUDE") == "" {
		t.Skip("set RUSUI_ACP_LIVE_CLAUDE=1 to run against claude-agent-acp")
	}
	cmd, err := GuestCommand(GuestClaude)
	if err != nil {
		t.Skip(err.Error())
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	c := &Client{In: stdout, Out: stdin, Perm: DenyUnmatched{}}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	init, err := c.Initialize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("initialize agent=%v protocol=%d", init.AgentInfo, init.ProtocolVersion)
	sid, err := c.SessionNew(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pr, err := c.SessionPrompt(ctx, sid, "Reply with exactly pong and stop.")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("session/prompt stopReason=%s", pr.StopReason)
	if pr.StopReason == "" {
		t.Fatal("empty stopReason")
	}
}

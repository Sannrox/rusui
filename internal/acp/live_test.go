package acp

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveGrokConformance(t *testing.T) {
	if os.Getenv("RUSUI_ACP_LIVE") == "" {
		t.Skip("set RUSUI_ACP_LIVE=1 to run against Grok stdio")
	}
	cmd, err := GrokCommand()
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
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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
	t.Logf("session/new %s", sid)
	pr, err := c.SessionPrompt(ctx, sid, "Reply with exactly pong and stop.")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("session/prompt stopReason=%s", pr.StopReason)
	if pr.StopReason == "" {
		t.Fatal("empty stopReason")
	}
}

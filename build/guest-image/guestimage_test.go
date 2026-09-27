package guestimage

import (
	"strings"
	"testing"
)

func TestDockerfileInstallsClaudeCodeCLINotACPAdapter(t *testing.T) {
	text := string(Dockerfile)
	if strings.Contains(text, "@agentclientprotocol/claude-agent-acp") {
		t.Fatal("guest image still installs claude-agent-acp")
	}
	if !strings.Contains(text, "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}") || !strings.Contains(text, "ARG CLAUDE_CODE_VERSION=2.1.283") {
		t.Fatal(text)
	}
}

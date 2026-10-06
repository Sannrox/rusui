package guestimage

import (
	"os"
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

func TestDockerfileBakesToolchainAndHeadlessBrowser(t *testing.T) {
	text := string(Dockerfile)
	ver, err := os.ReadFile("../../.go-version")
	if err != nil {
		t.Fatal(err)
	}
	goVersion := strings.TrimSpace(string(ver))
	for _, want := range []string{
		"ARG GO_VERSION=" + goVersion,
		"GO_SHA256_AMD64=",
		"GO_SHA256_ARM64=",
		"https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz",
		"/usr/local/go",
		"PATH=/usr/local/go/bin:",
		"make",
		"build-essential",
		"chromium",
		"fonts-liberation",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in Dockerfile", want)
		}
	}
}

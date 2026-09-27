package acp

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Supported guests (ADR 0002, ADR 0017 D1).
const (
	GuestGrok   = "grok"
	GuestClaude = "claude"
)

// ClaudeStdio is the pinned Claude Code CLI session (ADR 0025). The
// npm package claude-agent-acp is not the guest. --print is required:
// without it, --input-format is ignored and Claude starts interactive.
const ClaudeStdio = "claude --print --input-format stream-json --output-format stream-json --verbose --permission-mode default"

// GrokCommand builds the ADR 0002 spawn. PATH must contain `agent`.
func GrokCommand() (*exec.Cmd, error) {
	return GuestCommand(GuestGrok)
}

// GuestCommand builds the host spawn for a guest. Its binary must be on PATH.
func GuestCommand(guest string) (*exec.Cmd, error) {
	argv, err := SpawnArgsFor(guest)
	if err != nil {
		return nil, err
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return nil, fmt.Errorf("acp: %w", err)
	}
	cmd := exec.Command(path, argv[1:]...)
	cmd.Env = append(os.Environ(), "HOME="+os.Getenv("HOME"))
	return cmd, nil
}

func SpawnArgs() []string {
	return strings.Fields(GrokStdio)
}

// SpawnArgsFor is the stdio argv for a guest. Empty means Grok; an unknown
// guest fails closed. Claude and Codex use their own CLIs (ADR 0025).
func SpawnArgsFor(guest string) ([]string, error) {
	switch guest {
	case "", GuestGrok:
		return SpawnArgs(), nil
	case GuestClaude:
		return strings.Fields(ClaudeStdio), nil
	case "codex":
		return []string{"codex", "app-server", "--listen", "stdio://"}, nil
	default:
		return nil, fmt.Errorf("acp: unknown guest %q", guest)
	}
}

package acp

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/sannrox/rusui/internal/guest"
)

// Supported guests (ADR 0002, ADR 0017 D1, ADR 0060).
const (
	GuestGrok      = "grok"
	GuestClaude    = "claude"
	GuestShikigami = "shikigami"
)

// ClaudeStdio is the pinned Claude Code CLI session (ADR 0025). The
// npm package claude-agent-acp is not the guest. --print is required:
// without it, --input-format is ignored and Claude starts interactive.
// --permission-prompt-tool stdio sends every permission prompt to rusui as a
// can_use_tool control request; without it Claude denies them silently.
// The ask rule for Bash makes every shell command such a prompt: without
// it Claude Code runs commands it considers read-only (ls, cat, uname)
// without asking, so the project's permission rules never see them
// (#442). The JSON has no spaces because the command is split on them.
const ClaudeStdio = "claude --print --input-format stream-json --output-format stream-json --verbose --permission-mode default --permission-prompt-tool stdio --settings " + claudeAskSettings

const claudeAskSettings = `{"permissions":{"ask":["Bash"]}}`

// ShikigamiStdio is the ADR 0060 pin. --state is inside the environment
// (the spawn cwd). --always-approve is not the spawn.
const ShikigamiStdio = "shikigami --state ./state acp"

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
	return Command(argv)
}

// Command builds the selected registry process without a shell.
func Command(argv []string) (*exec.Cmd, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("acp: argv required")
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
// Shikigami uses the ADR 0060 pin (#507).
func SpawnArgsFor(name string) ([]string, error) {
	if name == "" {
		name = GuestGrok
	}
	entry, ok := guest.Builtin()[name]
	if !ok {
		return nil, fmt.Errorf("acp: unknown guest %q", name)
	}
	return entry.Argv, nil
}

package acp

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// GrokCommand builds the ADR 0002 spawn. PATH must contain `agent`.
func GrokCommand() (*exec.Cmd, error) {
	path, err := exec.LookPath("agent")
	if err != nil {
		return nil, fmt.Errorf("acp: %w", err)
	}
	args := SpawnArgs()[1:]
	cmd := exec.Command(path, args...)
	cmd.Env = append(os.Environ(), "HOME="+os.Getenv("HOME"))
	return cmd, nil
}

func SpawnArgs() []string {
	return strings.Fields(GrokStdio)
}

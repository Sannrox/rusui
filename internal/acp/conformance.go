package acp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/sannrox/rusui/internal/guest"
)

// Conform starts one isolated ACP process and exercises the subset the plane
// owns. It is deliberately independent of policy parsing: callers qualify a
// descriptor first, then persist ConformanceDigest(entry) beside it.
func Conform(ctx context.Context, entry guest.Entry, dir string) error {
	if entry.Protocol != guest.ProtocolACP || len(entry.Argv) == 0 || dir == "" {
		return fmt.Errorf("acp: conformance requires an ACP entry and workspace")
	}
	cmd, err := Command(entry.Argv)
	if err != nil {
		return err
	}
	cmd.Dir = dir
	home := dir + "/home"
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "RUSUI_ACP_CONFORMANCE=1"}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	client := &Client{In: stdout, Out: stdin, Perm: DenyUnmatched{}}
	if _, err := client.Initialize(ctx); err != nil {
		return err
	}
	session, err := client.SessionNew(ctx, dir)
	if err != nil {
		return err
	}
	promptCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	result, err := client.SessionPrompt(promptCtx, session, "Conformance probe. Reply once and stop.")
	if err != nil {
		return err
	}
	if result.StopReason == "" {
		return fmt.Errorf("acp: conformance prompt did not stop")
	}
	return nil
}

// Probe runs the configured version command without a shell.
func Probe(ctx context.Context, entry guest.Entry) error {
	if len(entry.Probe) == 0 {
		return fmt.Errorf("acp: probe required")
	}
	path, err := exec.LookPath(entry.Probe[0])
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, entry.Probe[1:]...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.TempDir()}
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

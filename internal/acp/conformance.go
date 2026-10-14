package acp

import (
	"context"
	"fmt"
	"io"
	"time"
)

// Conform exercises the ACP subset the plane owns over one guest's stdio:
// initialize, session/new in cwd, and one prompt that must stop. The caller
// spawns the guest where turns run it, inside the guest image, and persists
// guest.ConformanceDigest beside the entry once Conform passes.
func Conform(ctx context.Context, in io.Reader, out io.Writer, cwd string) error {
	client := &Client{In: in, Out: out, Perm: DenyUnmatched{}}
	if _, err := client.Initialize(ctx); err != nil {
		return err
	}
	session, err := client.SessionNew(ctx, cwd)
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

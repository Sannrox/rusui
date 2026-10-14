package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/guest"
)

// guestRuntime is the container exec surface turns use for a guest.
type guestRuntime interface {
	env.Runtime
	env.StdioExecutor
	ExecOutput(id string, cmd []string) ([]byte, bool, error)
}

// QualifyGuest runs the ACP conformance suite for entry inside its pinned
// image on the plane's container runtime and returns a receipt signed with
// the plane's conformance key (ADR 0082). The key never leaves the plane.
func (e *Engine) QualifyGuest(ctx context.Context, entry guest.Entry) (guest.Receipt, error) {
	if len(e.ConformanceKey) == 0 {
		return guest.Receipt{}, fmt.Errorf("conformance: plane has no %s", guest.ConformanceKeyEnv)
	}
	if entry.Protocol != guest.ProtocolACP || len(entry.Argv) == 0 || len(entry.Probe) == 0 || entry.Pin == "" {
		return guest.Receipt{}, fmt.Errorf("conformance: entry requires protocol acp, argv, probe, and pin")
	}
	c, ok := e.Container.(env.Container)
	if !ok {
		return guest.Receipt{}, fmt.Errorf("conformance: plane has no container driver")
	}
	rt, ok := c.RT.(guestRuntime)
	if !ok {
		return guest.Receipt{}, fmt.Errorf("conformance: container runtime cannot exec guests")
	}
	if _, pinned := guest.ImageDigest(entry.Image); !pinned {
		// Turns run exactly the qualified content, so name the resolved
		// image ID for the operator to pin.
		if r, ok := c.RT.(interface{ ImageID(string) (string, error) }); ok {
			if id, err := r.ImageID(entry.Image); err == nil {
				return guest.Receipt{}, fmt.Errorf("conformance: pin the guest image by digest, for example image: %s", id)
			}
		}
		return guest.Receipt{}, fmt.Errorf("conformance: pin the guest image by digest")
	}
	if err := runConformance(ctx, c, rt, entry); err != nil {
		return guest.Receipt{}, fmt.Errorf("conformance: %w", err)
	}
	return guest.SignReceipt(e.ConformanceKey, entry, e.now())
}

// runConformance starts a throwaway container from entry.Image and runs the
// version probe and the ACP subset in it through the same exec path as a turn.
func runConformance(ctx context.Context, c env.Container, rt guestRuntime, entry guest.Entry) error {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	// Runtime calls do not observe ctx. Creation may pull the image, so it
	// is awaited under the deadline; a container created after expiry is
	// removed when creation finally returns.
	type created struct {
		handle string
		err    error
	}
	made := make(chan created, 1)
	go func() {
		handle, err := c.CreateSpec(env.Spec{Name: "acp-conformance-" + hex.EncodeToString(suffix[:]), Image: entry.Image})
		made <- created{handle, err}
	}()
	var handle string
	select {
	case m := <-made:
		if m.err != nil {
			return m.err
		}
		handle = m.handle
	case <-ctx.Done():
		go func() {
			if m := <-made; m.err == nil {
				_ = c.Destroy(m.handle)
			}
		}()
		return fmt.Errorf("create: %w", ctx.Err())
	}
	defer func() { _ = c.Destroy(handle) }()
	// The runtime exec does not observe ctx; on expiry the deferred Destroy
	// removes the container, which ends the blocked exec.
	probe := make(chan error, 1)
	go func() {
		_, _, err := rt.ExecOutput(handle, entry.Probe)
		probe <- err
	}()
	select {
	case err := <-probe:
		if err != nil {
			return fmt.Errorf("probe: %w", err)
		}
	case <-ctx.Done():
		return fmt.Errorf("probe: %w", ctx.Err())
	}
	stdin, stdout, stop, err := rt.ExecStdio(handle, entry.Argv, []string{"RUSUI_ACP_CONFORMANCE=1"})
	if err != nil {
		return err
	}
	defer stop()
	return acp.Conform(ctx, stdout, stdin, env.WorkspaceDir)
}

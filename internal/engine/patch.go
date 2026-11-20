package engine

import (
	"fmt"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

func validateSessionPatch(patch []byte) error {
	if len(patch) == 0 {
		return nil
	}
	if len(patch) > env.WorkspaceUploadCap {
		return fmt.Errorf("oversize")
	}
	return nil
}

func (e *Engine) prepareAndPatch(d env.Driver, handle, repo, pin, hash string, sessionID int64) (setup *env.Capture, hooks []env.Capture, hit bool, err error) {
	setup, hooks, hit, err = e.prepareWorkspace(d, handle, repo, pin, hash)
	if err != nil {
		return setup, hooks, hit, err
	}
	if err := e.applySessionPatch(d, handle, sessionID); err != nil {
		return setup, hooks, hit, err
	}
	return setup, hooks, hit, nil
}

func (e *Engine) applySessionPatch(d env.Driver, handle string, sessionID int64) error {
	if sessionID == 0 {
		return nil
	}
	patch, err := store.SessionPatch(e.Store, sessionID)
	if err != nil || len(patch) == 0 {
		return err
	}
	ap, ok := d.(env.PatchApplier)
	if !ok {
		return fmt.Errorf("env: driver cannot apply patch")
	}
	if err := ap.ApplyPatch(handle, patch); err != nil {
		return fmt.Errorf("patch: %w", err)
	}
	return nil
}

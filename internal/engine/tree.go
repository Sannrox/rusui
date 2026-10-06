package engine

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/policy"
)

// TreeSource fetches a git pin onto the host. Implementations must not write
// credentials into dest.
type TreeSource interface {
	Fetch(repo, pin, dest string) error
}

type MemoryTree struct {
	Files map[string][]byte
	Err   error
	Calls int
}

func (m *MemoryTree) Fetch(repo, pin, dest string) error {
	m.Calls++
	if m.Err != nil {
		return m.Err
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	for rel, data := range m.Files {
		path := filepath.Join(dest, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return err
		}
	}
	_ = repo
	_ = pin
	return nil
}

func (e *Engine) snapshotRoot() string {
	if e.SnapshotRoot != "" {
		return e.SnapshotRoot
	}
	return filepath.Join("environments", ".snapshots")
}

// snapshotMarker names the file that marks a complete snapshot. A tree
// without it, or with an older stamp, is not a hit.
const snapshotMarker = ".rusui-snapshot"

// snapshotStamp is the marker body. A pin cached before setup's tree was
// kept wrote only the hash, so it does not match (#512).
func snapshotStamp(hash string) []byte {
	return []byte("prepared " + hash + "\n")
}

// prepareWorkspace fills a new environment's workspace. A snapshot hit
// places the prepared tree and skips setup. A miss places the git pin,
// runs setup, and stores the workspace after setup as the snapshot for
// hash (ADR 0007). Without a pin there is no snapshot, so setup runs on
// every new environment. The capture is nil when setup was not attempted.
func (e *Engine) prepareWorkspace(d env.Driver, handle, repo, pin, hash string) (*env.Capture, error) {
	if policy.IsProjectKey(repo) {
		// Empty workspace, no .git, no setup (ADR 0048).
		return nil, nil
	}
	if e.Tree == nil || repo == "" || pin == "" || hash == "" {
		return runSetup(d, handle, hash)
	}
	root := e.snapshotRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	dir := filepath.Join(root, hash)
	if snapshotComplete(dir, hash) {
		return nil, placeTree(d, handle, dir)
	}
	staging, err := os.MkdirTemp(root, "prep-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := e.Tree.Fetch(repo, pin, staging); err != nil {
		return nil, err
	}
	if err := placeTree(d, handle, staging); err != nil {
		return nil, err
	}
	c, err := runSetup(d, handle, hash)
	if err != nil {
		return c, err
	}
	tree := staging
	if c != nil && c.Ran {
		capturer, ok := d.(env.TreeCapturer)
		if !ok {
			e.exception("snapshot " + hash + ": driver cannot capture tree; setup will run again")
			return c, nil
		}
		tree, err = os.MkdirTemp(root, "capture-")
		if err != nil {
			e.exception("snapshot " + hash + ": " + err.Error())
			return c, nil
		}
		defer func() { _ = os.RemoveAll(tree) }()
		if err := capturer.CaptureTree(handle, tree); err != nil {
			e.exception("snapshot " + hash + ": capture: " + err.Error())
			return c, nil
		}
	}
	// This environment is already prepared; a snapshot that cannot be
	// stored costs the next environment a setup run, not this one.
	if err := e.storeSnapshot(dir, hash, tree); err != nil {
		e.exception("snapshot " + hash + ": store: " + err.Error())
	}
	return c, nil
}

func placeTree(d env.Driver, handle, src string) error {
	placer, ok := d.(interface {
		PlaceTree(handle, srcDir string) error
	})
	if !ok {
		return fmt.Errorf("env: driver cannot place tree")
	}
	return placer.PlaceTree(handle, src)
}

// runSetup runs `.agents/setup` when the driver has one.
func runSetup(d env.Driver, handle, hash string) (*env.Capture, error) {
	p, ok := d.(env.Preparer)
	if !ok {
		return nil, nil
	}
	c, err := p.Setup(handle, hash)
	return &c, err
}

func snapshotComplete(dir, hash string) bool {
	b, err := os.ReadFile(filepath.Join(dir, snapshotMarker))
	return err == nil && string(b) == string(snapshotStamp(hash))
}

// storeSnapshot stamps tree and renames it to dir, so a crash never
// leaves a partial tree marked complete. When another environment stored
// the same hash first, tree is left for the caller to remove.
func (e *Engine) storeSnapshot(dir, hash, tree string) error {
	marker := filepath.Join(tree, snapshotMarker)
	// The workspace may carry its own marker, even as a symlink.
	if err := os.RemoveAll(marker); err != nil {
		return err
	}
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(snapshotStamp(hash)); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	e.snapshotMu.Lock()
	defer e.snapshotMu.Unlock()
	if snapshotComplete(dir, hash) {
		return nil
	}
	// An incomplete dir is never placed from, so removing it is safe.
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return os.Rename(tree, dir)
}

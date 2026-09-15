package engine

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sannrox/rusui/internal/env"
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

func (e *Engine) materialize(repo, pin, hash string, d env.Driver, handle string) error {
	if e.Tree == nil || repo == "" || pin == "" || hash == "" {
		return nil
	}
	cache, err := e.prepareCache(repo, pin, hash)
	if err != nil {
		return err
	}
	placer, ok := d.(interface {
		PlaceTree(handle, srcDir string) error
	})
	if !ok {
		return fmt.Errorf("env: driver cannot place tree")
	}
	return placer.PlaceTree(handle, cache)
}

func (e *Engine) prepareCache(repo, pin, hash string) (string, error) {
	root := e.snapshotRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	dir := filepath.Join(root, hash)
	if _, err := os.Stat(filepath.Join(dir, ".rusui-snapshot")); err == nil {
		return dir, nil
	}
	tmp, err := os.MkdirTemp(root, "prep-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := e.Tree.Fetch(repo, pin, tmp); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmp, ".rusui-snapshot"), []byte(hash+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return "", err
	}
	return dir, nil
}

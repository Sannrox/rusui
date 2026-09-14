package env

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	KindProcess   = "process"
	KindContainer = "container"
	SetupPath     = ".agents/setup"
	ResumePath    = ".agents/resume"
)

// Driver owns an environment handle. Process uses a workspace directory.
// Container uses a Docker/Podman-compatible runtime.
type Driver interface {
	Kind() string
	Create(name string) (handle string, err error)
	Sleep(handle string) error
	Wake(handle string) error
	Destroy(handle string) error
}

type Process struct {
	Root string
}

func (p Process) Kind() string { return KindProcess }

func (p Process) Create(name string) (string, error) {
	if p.Root == "" {
		return "", fmt.Errorf("env: process root required")
	}
	dir := filepath.Join(p.Root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, ".rusui-env"), []byte("ready\n"), 0o600); err != nil {
		return "", err
	}
	return dir, nil
}

func (p Process) Sleep(handle string) error {
	return writeState(handle, "sleeping")
}

func (p Process) Wake(handle string) error {
	return writeState(handle, "ready")
}

func (p Process) Destroy(handle string) error {
	if handle == "" {
		return nil
	}
	return os.RemoveAll(handle)
}

func writeState(handle, state string) error {
	if handle == "" {
		return fmt.Errorf("env: empty handle")
	}
	return os.WriteFile(filepath.Join(handle, ".rusui-env"), []byte(state+"\n"), 0o600)
}

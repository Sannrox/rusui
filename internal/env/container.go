package env

import (
	"fmt"
	"io"
)

// Runtime is the Docker/Podman-shaped control plane. Tests use FakeRuntime.
type Runtime interface {
	CreateAndStart(Spec) (id string, err error)
	Stop(id string) error
	Start(id string) error
	Remove(id string) error
	HasFile(id, path string) bool
	ReadFile(id, path string) ([]byte, error)
	Exec(id string, cmd []string) error
}

// StdioExecutor execs a command in a container with attached stdin/stdout.
type StdioExecutor interface {
	ExecStdio(handle string, argv, env []string) (stdin io.WriteCloser, stdout io.ReadCloser, stop func(), err error)
}

type Spec struct {
	Name        string
	Image       string
	CPUMillis   int
	MemoryBytes int64
	Network     string
	ExtraHosts  []string
	DisableIPv6 bool
	CAFile      string
}

type Container struct {
	RT     Runtime
	Image  string
	CAFile string
}

func (c Container) Kind() string { return KindContainer }

func (c Container) Create(name string) (string, error) {
	return c.CreateSpec(Spec{Name: name})
}

func (c Container) CreateSpec(spec Spec) (string, error) {
	if c.RT == nil {
		return "", fmt.Errorf("env: container runtime required")
	}
	if spec.Image == "" {
		spec.Image = c.Image
	}
	if spec.Image == "" {
		return "", fmt.Errorf("env: guest image required")
	}
	if c.CAFile != "" && spec.CAFile == "" {
		spec.CAFile = c.CAFile
	}
	ApplyTrustedNetwork(&spec)
	return c.RT.CreateAndStart(spec)
}

type SpecCreator interface {
	CreateSpec(Spec) (string, error)
}

func (c Container) Sleep(handle string) error {
	return c.RT.Stop(handle)
}

func (c Container) Wake(handle string) error {
	return c.RT.Start(handle)
}

func (c Container) Destroy(handle string) error {
	if handle == "" {
		return nil
	}
	return c.RT.Remove(handle)
}

func (c Container) KillGuest(handle string) error {
	if handle == "" || c.RT == nil {
		return nil
	}
	return c.RT.Exec(handle, []string{"pkill", "-TERM", "-P", "1"})
}

func (c Container) ExecStdio(handle string, argv, env []string) (io.WriteCloser, io.ReadCloser, func(), error) {
	x, ok := c.RT.(StdioExecutor)
	if !ok {
		return nil, nil, nil, fmt.Errorf("env: runtime cannot exec stdio")
	}
	return x.ExecStdio(handle, argv, env)
}

func (c Container) PlaceTree(handle, srcDir string) error {
	p, ok := c.RT.(interface {
		PlaceTree(handle, srcDir string) error
	})
	if !ok {
		return fmt.Errorf("env: runtime cannot place tree")
	}
	return p.PlaceTree(handle, srcDir)
}

// Setup runs `.agents/setup` when present. The engine calls this only
// when the environment's source hash is new.
func (c Container) Setup(handle, _ string) error {
	if !c.RT.HasFile(handle, SetupPath) {
		return nil
	}
	return c.RT.Exec(handle, []string{"/bin/sh", SetupPath})
}

// Resume runs `.agents/resume` after wake when the file exists.
func (c Container) Resume(handle string) error {
	if !c.RT.HasFile(handle, ResumePath) {
		return nil
	}
	return c.RT.Exec(handle, []string{"/bin/sh", ResumePath})
}

type Preparer interface {
	Setup(handle, sourceHash string) error
}

type Resumer interface {
	Resume(handle string) error
}

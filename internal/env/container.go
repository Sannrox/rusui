package env

import "fmt"

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

type Spec struct {
	Name        string
	Image       string
	CPUMillis   int
	MemoryBytes int64
}

type Container struct {
	RT    Runtime
	Image string
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
		spec.Image = "rusui-guest:local"
	}
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

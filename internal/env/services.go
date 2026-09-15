package env

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
)

const (
	ServicesRusuiPath = ".rusui/services.yaml"
	servicePIDDir     = ".rusui/svc"
)

var serviceName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ServiceCtl starts and stops declared services.yaml processes.
type ServiceCtl interface {
	StartServices(handle string) error
	StopServices(handle string) error
}

type Service struct {
	Name    string
	Command string
	Cwd     string
	Env     map[string]string
}

type servicesFile struct {
	Services map[string]serviceSpec `yaml:"services"`
}

type serviceSpec struct {
	Command string            `yaml:"command"`
	Cwd     string            `yaml:"cwd"`
	Env     map[string]string `yaml:"env"`
}

func ParseServices(data []byte) ([]Service, error) {
	var doc servicesFile
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("env: services.yaml: %w", err)
	}
	if len(doc.Services) == 0 {
		return nil, fmt.Errorf("env: services.yaml: services mapping is required")
	}
	names := make([]string, 0, len(doc.Services))
	for name := range doc.Services {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]Service, 0, len(names))
	for _, name := range names {
		if !serviceName.MatchString(name) {
			return nil, fmt.Errorf("env: services.yaml: invalid service name %q", name)
		}
		spec := doc.Services[name]
		if strings.TrimSpace(spec.Command) == "" {
			return nil, fmt.Errorf("env: services.yaml: service %q missing command", name)
		}
		out = append(out, Service{Name: name, Command: spec.Command, Cwd: spec.Cwd, Env: spec.Env})
	}
	return out, nil
}

func loadServices(read func(path string) ([]byte, error)) ([]Service, error) {
	data, err := read(ServicesRusuiPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return ParseServices(data)
}

func readWorkspaceFile(root, path string) ([]byte, error) {
	return os.ReadFile(filepath.Join(root, path))
}

func (p Process) StartServices(handle string) error {
	svcs, err := loadServices(func(path string) ([]byte, error) {
		return readWorkspaceFile(handle, path)
	})
	if err != nil || len(svcs) == 0 {
		return err
	}
	if err := os.MkdirAll(filepath.Join(handle, servicePIDDir), 0o700); err != nil {
		return err
	}
	for _, svc := range svcs {
		if err := startProcessService(handle, svc); err != nil {
			_ = p.StopServices(handle)
			return err
		}
	}
	return nil
}

func (p Process) StopServices(handle string) error {
	dir := filepath.Join(handle, servicePIDDir)
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var first error
	for _, ent := range ents {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".pid") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || pid <= 0 {
			continue
		}
		if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !os.IsNotExist(err) {
			if first == nil {
				first = err
			}
		}
		_ = os.Remove(filepath.Join(dir, ent.Name()))
	}
	return first
}

func startProcessService(handle string, svc Service) error {
	cmd := exec.Command("sh", "-c", svc.Command)
	cmd.Dir = handle
	if svc.Cwd != "" {
		cmd.Dir = filepath.Join(handle, svc.Cwd)
	}
	cmd.Env = serviceEnv(svc.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	return os.WriteFile(filepath.Join(handle, servicePIDDir, svc.Name+".pid"), []byte(strconv.Itoa(pid)+"\n"), 0o600)
}

func serviceEnv(extra map[string]string) []string {
	env := []string{"PATH=" + os.Getenv("PATH")}
	if home := os.Getenv("HOME"); home != "" {
		env = append(env, "HOME="+home)
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		env = append(env, k+"="+extra[k])
	}
	return env
}

func (c Container) StartServices(handle string) error {
	svcs, err := loadServices(func(path string) ([]byte, error) {
		return readRuntimeFile(c.RT, handle, path)
	})
	if err != nil || len(svcs) == 0 {
		return err
	}
	for _, svc := range svcs {
		cmd := []string{"/bin/sh", "-c", svc.Command}
		if svc.Cwd != "" {
			cmd = []string{"/bin/sh", "-c", "cd \"$1\" && shift && eval \"$1\"", "rusui-svc", svc.Cwd, svc.Command}
		}
		if err := c.RT.Exec(handle, cmd); err != nil {
			return err
		}
	}
	return nil
}

func (c Container) StopServices(string) error {
	return nil
}

func readRuntimeFile(rt Runtime, id, path string) ([]byte, error) {
	if rt == nil || !rt.HasFile(id, path) {
		return nil, os.ErrNotExist
	}
	return rt.ReadFile(id, path)
}

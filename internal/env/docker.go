package env

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const workspaceDir = "/workspace"

// DockerCLI is a Docker or Podman CLI runtime. Bin defaults to docker.
type DockerCLI struct {
	Bin    string
	CAFile string
}

func LookRuntime() (Runtime, error) {
	for _, name := range []string{"docker", "podman"} {
		path, err := exec.LookPath(name)
		if err == nil {
			return DockerCLI{Bin: path}, nil
		}
	}
	return nil, fmt.Errorf("env: docker or podman not found")
}

func (d DockerCLI) bin() string {
	if d.Bin != "" {
		return d.Bin
	}
	return "docker"
}

func (d DockerCLI) ensureNetwork(name string) error {
	if name == "" {
		return fmt.Errorf("env: trusted network required")
	}
	if _, err := d.run("network", "inspect", name); err == nil {
		return nil
	}
	if _, err := d.run("network", "create", name); err != nil {
		if _, err2 := d.run("network", "inspect", name); err2 == nil {
			return nil
		}
		return fmt.Errorf("env: create network %s: %w", name, err)
	}
	return nil
}

func (d DockerCLI) run(args ...string) ([]byte, error) {
	cmd := exec.Command(d.bin(), args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return out, fmt.Errorf("docker %s: %w", args[0], err)
		}
		return out, fmt.Errorf("docker %s: %s", args[0], msg)
	}
	return out, nil
}

func (d DockerCLI) CreateAndStart(spec Spec) (string, error) {
	if spec.Image == "" {
		return "", fmt.Errorf("env: guest image required")
	}
	if d.CAFile != "" && spec.CAFile == "" {
		spec.CAFile = d.CAFile
	}
	if spec.Network == "" {
		ApplyTrustedNetwork(&spec)
	}
	if err := d.ensureNetwork(spec.Network); err != nil {
		return "", err
	}
	args := []string{"run", "-d"}
	args = append(args, TrustedRunArgs(spec)...)
	args = append(args, "--entrypoint", "sleep")
	if spec.Name != "" {
		args = append(args, "--name", "rusui-"+spec.Name)
	}
	if spec.CPUMillis > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(float64(spec.CPUMillis)/1000, 'f', 3, 64))
	}
	if spec.MemoryBytes > 0 {
		args = append(args, "--memory", strconv.FormatInt(spec.MemoryBytes, 10))
	}
	args = append(args, spec.Image, "infinity")
	out, err := d.run(args...)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		return "", fmt.Errorf("docker run: empty id")
	}
	if _, err := d.run("exec", id, "mkdir", "-p", workspaceDir); err != nil {
		_ = d.Remove(id)
		return "", err
	}
	return id, nil
}

func (d DockerCLI) Stop(id string) error {
	_, err := d.run("stop", id)
	return err
}

func (d DockerCLI) Start(id string) error {
	_, err := d.run("start", id)
	return err
}

func (d DockerCLI) Remove(id string) error {
	if id == "" {
		return nil
	}
	_, err := d.run("rm", "-f", id)
	return err
}

func (d DockerCLI) HasFile(id, path string) bool {
	p := workspacePath(path)
	_, err := d.run("exec", id, "test", "-f", p)
	return err == nil
}

func (d DockerCLI) ReadFile(id, path string) ([]byte, error) {
	cmd := exec.Command(d.bin(), "exec", id, "cat", workspacePath(path))
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (d DockerCLI) Exec(id string, cmd []string) error {
	args := append([]string{"exec", "-w", workspaceDir, id}, cmd...)
	_, err := d.run(args...)
	return err
}

func (d DockerCLI) ExecStdio(handle string, argv, env []string) (io.WriteCloser, io.ReadCloser, func(), error) {
	args := []string{"exec", "-i", "-w", workspaceDir}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	args = append(args, handle)
	args = append(args, argv...)
	cmd := exec.Command(d.bin(), args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}
	stop := func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}
	return stdin, stdout, stop, nil
}

func (d DockerCLI) PlaceTree(handle, srcDir string) error {
	if handle == "" || srcDir == "" {
		return fmt.Errorf("env: place tree requires handle and src")
	}
	if !strings.HasSuffix(srcDir, string(os.PathSeparator)) && !strings.HasSuffix(srcDir, ".") {
		srcDir = srcDir + string(os.PathSeparator) + "."
	}
	_, err := d.run("cp", srcDir, handle+":"+workspaceDir)
	return err
}

func workspacePath(path string) string {
	if strings.HasPrefix(path, "/") {
		return path
	}
	return workspaceDir + "/" + path
}

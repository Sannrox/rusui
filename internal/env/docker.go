package env

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// WorkspaceDir is the session workspace inside a container guest.
const WorkspaceDir = "/workspace"

const workspaceDir = WorkspaceDir

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

// NetworkExists reports whether the named container network exists.
func (d DockerCLI) NetworkExists(name string) bool {
	_, err := d.run("network", "inspect", name)
	return err == nil
}

// ImageExists reports whether a local image with this tag exists.
func (d DockerCLI) ImageExists(tag string) bool {
	_, err := d.run("image", "inspect", tag)
	return err == nil
}

// BuildImage builds tag from a Dockerfile given on stdin (no build context).
// fresh pulls the base and ignores the build cache.
func (d DockerCLI) BuildImage(tag string, dockerfile []byte, fresh bool) error {
	args := []string{"build", "-t", tag}
	if fresh {
		args = append(args, "--pull", "--no-cache")
	}
	cmd := exec.Command(d.bin(), append(args, "-")...)
	cmd.Stdin = bytes.NewReader(dockerfile)
	if out, err := cmd.CombinedOutput(); err != nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		return fmt.Errorf("docker build %s: %w: %s", tag, err, lines[len(lines)-1])
	}
	return nil
}

// ImageID is the local image ID (sha256:…) of tag.
func (d DockerCLI) ImageID(tag string) (string, error) {
	out, err := d.run("image", "inspect", "--format", "{{.Id}}", tag)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// TagImage adds dst as another tag of src.
func (d DockerCLI) TagImage(src, dst string) error {
	_, err := d.run("tag", src, dst)
	return err
}

// EnsureNetwork creates the named network when it is missing.
func (d DockerCLI) EnsureNetwork(name string) error {
	return d.ensureNetwork(name)
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
	id, err := containerID(out)
	if err != nil {
		return "", err
	}
	if _, err := d.run("exec", id, "mkdir", "-p", workspaceDir); err != nil {
		_ = d.Remove(id)
		return "", err
	}
	return id, nil
}

// containerID takes the last 12–64 hex line of `docker run -d` output so
// image-pull progress on CombinedOutput is not treated as the handle.
func containerID(out []byte) (string, error) {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "", fmt.Errorf("docker run: empty id")
	}
	lines := strings.Split(s, "\n")
	for _, line := range slices.Backward(lines) {
		id := strings.TrimSpace(line)
		if isContainerID(id) {
			return id, nil
		}
	}
	return "", fmt.Errorf("docker run: no container id")
}

func isContainerID(id string) bool {
	if n := len(id); n < 12 || n > 64 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// dockerForwardJS pipes stdio to 127.0.0.1:port inside the guest.
// `node -e SCRIPT PORT` puts PORT at process.argv[1].
const dockerForwardJS = `const n=require("net");const s=n.connect({host:"127.0.0.1",port:+process.argv[1]});process.stdin.pipe(s);s.pipe(process.stdout,{end:false});let once=false;const done=()=>{if(once)return;once=true;process.stdout.end(()=>process.exit(0))};s.on("end",done);s.on("close",done);s.on("error",()=>process.exit(1));process.stdin.on("end",()=>s.end());`

var dockerForwards sync.Map // id/port -> net.Listener

func (d DockerCLI) GuestAddr(id string, port int) (string, error) {
	if err := validGuestPort(port); err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("env: empty handle")
	}
	key := id + "/" + strconv.Itoa(port)
	if v, ok := dockerForwards.Load(key); ok {
		return v.(net.Listener).Addr().String(), nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	if actual, loaded := dockerForwards.LoadOrStore(key, ln); loaded {
		_ = ln.Close()
		return actual.(net.Listener).Addr().String(), nil
	}
	go d.serveForward(ln, id, port)
	return ln.Addr().String(), nil
}

func (d DockerCLI) serveForward(ln net.Listener, id string, port int) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go d.proxyConn(id, port, c)
	}
}

func (d DockerCLI) proxyConn(id string, port int, c net.Conn) {
	defer func() { _ = c.Close() }()
	cmd := exec.Command(d.bin(), "exec", "-i", id, "node", "-e", dockerForwardJS, strconv.Itoa(port))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}
	go func() {
		_, _ = io.Copy(stdin, c)
		_ = stdin.Close()
	}()
	_, _ = io.Copy(c, stdout)
	_ = c.Close()
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
}

func closeDockerForwards(id string) {
	prefix := id + "/"
	dockerForwards.Range(func(k, v any) bool {
		key, _ := k.(string)
		if id == "" || key == id || strings.HasPrefix(key, prefix) {
			if ln, ok := v.(net.Listener); ok {
				_ = ln.Close()
			}
			dockerForwards.Delete(k)
		}
		return true
	})
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
	closeDockerForwards(id)
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
	// Values travel through the CLI's environment, never its argv, so
	// credentials do not appear in the host process list.
	args := []string{"exec", "-i", "-w", workspaceDir}
	cliEnv := os.Environ()
	for _, e := range env {
		k, _, _ := strings.Cut(e, "=")
		args = append(args, "-e", k)
		cliEnv = append(cliEnv, e)
	}
	args = append(args, handle)
	args = append(args, argv...)
	cmd := exec.Command(d.bin(), args...)
	cmd.Env = cliEnv
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

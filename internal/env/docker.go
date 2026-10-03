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
	if spec.Network != NoNetwork {
		if err := d.ensureNetwork(spec.Network); err != nil {
			return "", err
		}
	}
	args := []string{"run", "-d"}
	args = append(args, TrustedRunArgs(spec)...)
	// PID 1 must stay this keepalive: KillGuest signals every other
	// process, so --init or another entrypoint would stop the container
	// on cancel (#441).
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

// previewLink is the plane's inbound-only guest link to one container:
// one host listener per forwarded guest port, each accepted connection a
// stream to that port on the guest's loopback (ADR 0047). A link is
// started outside mu so a guest that never becomes ready cannot hold up
// other previews or environment lifecycle.
type previewLink struct {
	mu        sync.Mutex
	link      *Link
	starting  chan struct{}
	closed    bool
	listeners map[int]net.Listener
}

var dockerForwards sync.Map // container id -> *previewLink

// GuestAddr returns a plane-local address that reaches port on the
// guest's loopback through the guest link. A link that has ended is
// restarted on the next connection.
func (d DockerCLI) GuestAddr(id string, port int) (string, error) {
	return guestAddr(d, id, port)
}

func guestAddr(x StdioExecutor, id string, port int) (string, error) {
	if err := validGuestPort(port); err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("env: empty handle")
	}
	v, _ := dockerForwards.LoadOrStore(id, &previewLink{listeners: map[int]net.Listener{}})
	p := v.(*previewLink)
	if _, err := p.current(x, id); err != nil {
		return "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return "", fmt.Errorf("env: guest link closed")
	}
	if ln, ok := p.listeners[port]; ok {
		return ln.Addr().String(), nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	p.listeners[port] = ln
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			l, err := p.current(x, id)
			if err != nil || l.Forward(c, port) != nil {
				_ = c.Close()
			}
		}
	}()
	return ln.Addr().String(), nil
}

// current returns a live link, starting one if none is live. Concurrent
// callers wait for a single start.
func (p *previewLink) current(x StdioExecutor, id string) (*Link, error) {
	p.mu.Lock()
	for {
		if p.closed {
			p.mu.Unlock()
			return nil, fmt.Errorf("env: guest link closed")
		}
		if p.link != nil {
			select {
			case <-p.link.Done():
			default:
				l := p.link
				p.mu.Unlock()
				return l, nil
			}
		}
		if p.starting == nil {
			break
		}
		ch := p.starting
		p.mu.Unlock()
		<-ch
		p.mu.Lock()
	}
	ch := make(chan struct{})
	p.starting = ch
	p.mu.Unlock()
	l, err := StartLink(x, id, nil)
	p.mu.Lock()
	p.starting = nil
	close(ch)
	if err == nil && p.closed {
		p.mu.Unlock()
		l.Close()
		return nil, fmt.Errorf("env: guest link closed")
	}
	if err == nil {
		p.link = l
	}
	p.mu.Unlock()
	return l, err
}

func (d DockerCLI) closeForwards(id string) { closeForwards(id) }

func closeForwards(id string) {
	dockerForwards.Range(func(k, v any) bool {
		key, _ := k.(string)
		if id == "" || key == id {
			p := v.(*previewLink)
			p.mu.Lock()
			p.closed = true
			for _, ln := range p.listeners {
				_ = ln.Close()
			}
			l := p.link
			p.link = nil
			p.mu.Unlock()
			if l != nil {
				l.Close()
			}
			dockerForwards.Delete(k)
		}
		return true
	})
}

func (d DockerCLI) Stop(id string) error {
	d.closeForwards(id)
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
	d.closeForwards(id)
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

// ExecOutput is Exec that returns the last CaptureLimit bytes of combined
// stdout and stderr. The error names the exit status, not the output.
func (d DockerCLI) ExecOutput(id string, cmd []string) ([]byte, bool, error) {
	args := append([]string{"exec", "-w", workspaceDir, id}, cmd...)
	c := exec.Command(d.bin(), args...)
	// One comparable writer for both streams makes os/exec share a single
	// pipe and copy goroutine, so tailBuffer needs no lock.
	buf := &tailBuffer{limit: CaptureLimit}
	c.Stdout = buf
	c.Stderr = buf
	err := c.Run()
	out, truncated := buf.result()
	if err != nil {
		err = fmt.Errorf("docker exec: %w", err)
	}
	return out, truncated, err
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

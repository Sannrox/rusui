package env

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ScriptRunner runs a plane-stored hook inside an environment. The script
// is stdin to `/bin/sh`, so it does not appear on argv.
type ScriptRunner interface {
	ExecScript(handle, script string) (out []byte, truncated bool, err error)
}

func (c Container) ExecScript(handle, script string) ([]byte, bool, error) {
	x, ok := c.RT.(ScriptRunner)
	if !ok {
		return nil, false, fmt.Errorf("env: runtime cannot run script")
	}
	return x.ExecScript(handle, script)
}

func (d DockerCLI) ExecScript(handle, script string) ([]byte, bool, error) {
	if handle == "" {
		return nil, false, fmt.Errorf("env: empty handle")
	}
	cmd := exec.Command(d.bin(), "exec", "-i", "-w", workspaceDir, handle, "/bin/sh")
	cmd.Stdin = strings.NewReader(script)
	buf := &tailBuffer{limit: CaptureLimit}
	cmd.Stdout = buf
	cmd.Stderr = buf
	err := cmd.Run()
	out, truncated := buf.result()
	if err != nil {
		err = fmt.Errorf("docker exec: %w", err)
	}
	return out, truncated, err
}

func (f *FakeRuntime) ExecScript(handle, script string) ([]byte, bool, error) {
	return f.ExecOutput(handle, []string{"/bin/sh", "-c", script})
}

func (p Process) ExecScript(handle, script string) ([]byte, bool, error) {
	if handle == "" {
		return nil, false, fmt.Errorf("env: empty handle")
	}
	cmd := exec.Command("/bin/sh")
	cmd.Dir = handle
	cmd.Stdin = strings.NewReader(script)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + handle}
	buf := &tailBuffer{limit: CaptureLimit}
	cmd.Stdout = buf
	cmd.Stderr = buf
	err := cmd.Run()
	out, truncated := buf.result()
	return out, truncated, err
}

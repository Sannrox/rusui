package env

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// PatchApplier applies a git patch onto a prepared workspace.
type PatchApplier interface {
	ApplyPatch(handle string, patch []byte) error
}

func (p Process) ApplyPatch(handle string, patch []byte) error {
	if handle == "" {
		return fmt.Errorf("env: empty handle")
	}
	return ApplyGitPatch(handle, patch)
}

func (c Container) ApplyPatch(handle string, patch []byte) error {
	x, ok := c.RT.(PatchApplier)
	if !ok {
		return fmt.Errorf("env: runtime cannot apply patch")
	}
	return x.ApplyPatch(handle, patch)
}

func (d DockerCLI) ApplyPatch(id string, patch []byte) error {
	if id == "" {
		return fmt.Errorf("env: empty handle")
	}
	if len(patch) == 0 {
		return nil
	}
	if err := d.gitApply(id, patch, true); err != nil {
		return err
	}
	return d.gitApply(id, patch, false)
}

func (d DockerCLI) gitApply(id string, patch []byte, check bool) error {
	args := []string{"exec", "-i", "-w", workspaceDir, id, "git", "-c", "safe.directory=" + workspaceDir, "apply"}
	if check {
		args = append(args, "--check")
	}
	cmd := exec.Command(d.bin(), args...)
	cmd.Stdin = bytes.NewReader(patch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return gitApplyError(out, err)
	}
	return nil
}

func (f *FakeRuntime) ApplyPatch(id string, patch []byte) error {
	if len(patch) == 0 {
		return nil
	}
	dir, err := os.MkdirTemp("", "rusui-patch-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := f.CaptureTree(id, dir); err != nil {
		return err
	}
	if err := ApplyGitPatch(dir, patch); err != nil {
		return err
	}
	f.mu.Lock()
	if f.Files != nil {
		f.Files[id] = map[string]bool{}
	}
	if f.Contents != nil {
		f.Contents[id] = map[string][]byte{}
	}
	f.mu.Unlock()
	return f.PlaceTree(id, dir)
}

// ApplyGitPatch checks then applies a unified git patch in dir. A failed
// check leaves the tree unchanged. git apply is atomic without --reject.
func ApplyGitPatch(dir string, patch []byte) error {
	if dir == "" {
		return fmt.Errorf("env: empty handle")
	}
	if len(patch) == 0 {
		return nil
	}
	if err := gitApply(dir, patch, true); err != nil {
		return err
	}
	return gitApply(dir, patch, false)
}

func gitApply(dir string, patch []byte, check bool) error {
	args := []string{"apply"}
	if check {
		args = append(args, "--check")
	}
	cmd := gitCmd(dir, args...)
	cmd.Stdin = bytes.NewReader(patch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return gitApplyError(out, err)
	}
	return nil
}

func gitApplyError(out []byte, err error) error {
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		return fmt.Errorf("env: git apply: %w", err)
	}
	return fmt.Errorf("env: git apply: %s", msg)
}

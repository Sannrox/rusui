package env

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const WorkspaceUploadCap = 32 << 20

type WorkspaceWriter interface {
	WriteFile(handle, rel string, r io.Reader, max int64) error
}

func SafeWorkspaceRel(p string) (string, bool) {
	if p == "" || strings.Contains(p, "\\") || filepath.IsAbs(p) {
		return "", false
	}
	for part := range strings.SplitSeq(p, "/") {
		if part == ".." {
			return "", false
		}
	}
	clean := filepath.Clean("/" + p)
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}

func (p Process) WriteFile(handle, rel string, r io.Reader, max int64) error {
	if handle == "" {
		return fmt.Errorf("env: workspace missing")
	}
	clean, ok := SafeWorkspaceRel(rel)
	if !ok {
		return fmt.Errorf("env: path")
	}
	full := filepath.Join(handle, clean)
	base := filepath.Clean(handle)
	if full != base && !strings.HasPrefix(full, base+string(os.PathSeparator)) {
		return fmt.Errorf("env: path")
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return err
	}
	fd, err := unix.Open(full, unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), clean)
	defer func() { _ = f.Close() }()
	if max <= 0 {
		max = WorkspaceUploadCap
	}
	n, err := io.Copy(f, io.LimitReader(r, max+1))
	if err != nil {
		return err
	}
	if n > max {
		_ = os.Remove(full)
		return fmt.Errorf("env: oversized")
	}
	return nil
}

type workspaceFileRuntime interface {
	WriteFile(id, rel string, r io.Reader, max int64) error
}

type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (c Container) WriteFile(handle, rel string, r io.Reader, max int64) error {
	if handle == "" {
		return fmt.Errorf("env: empty handle")
	}
	clean, ok := SafeWorkspaceRel(rel)
	if !ok {
		return fmt.Errorf("env: path")
	}
	if max <= 0 {
		max = WorkspaceUploadCap
	}
	if w, ok := c.RT.(workspaceFileRuntime); ok {
		return w.WriteFile(handle, clean, r, max)
	}
	d, ok := c.RT.(DockerCLI)
	if !ok {
		return fmt.Errorf("env: runtime cannot write files")
	}
	return d.WriteFile(handle, clean, r, max)
}

func (d DockerCLI) WriteFile(id, rel string, r io.Reader, max int64) error {
	if max <= 0 {
		max = WorkspaceUploadCap
	}
	dest := workspacePath(rel)
	dir := path.Dir(dest)
	if dir != "." && dir != workspaceDir {
		if _, err := d.run("exec", id, "mkdir", "-p", dir); err != nil {
			return err
		}
	}
	cr := &countReader{r: io.LimitReader(r, max+1)}
	cmd := exec.Command(d.bin(), "exec", "-i", id, "tee", dest)
	cmd.Stdin = cr
	cmd.Stdout = io.Discard
	err := cmd.Run()
	if cr.n > max {
		_, _ = d.run("exec", id, "rm", "-f", dest)
		return fmt.Errorf("env: oversized")
	}
	if err != nil {
		return err
	}
	return nil
}

func (f *FakeRuntime) WriteFile(id, rel string, r io.Reader, max int64) error {
	if max <= 0 {
		max = WorkspaceUploadCap
	}
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(r, max+1))
	if err != nil {
		return err
	}
	if n > max {
		return fmt.Errorf("env: oversized")
	}
	data := buf.Bytes()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Contents == nil {
		f.Contents = map[string]map[string][]byte{}
	}
	if f.Contents[id] == nil {
		f.Contents[id] = map[string][]byte{}
	}
	if f.Files == nil {
		f.Files = map[string]map[string]bool{}
	}
	if f.Files[id] == nil {
		f.Files[id] = map[string]bool{}
	}
	f.Contents[id][rel] = data
	f.Files[id][rel] = true
	return nil
}

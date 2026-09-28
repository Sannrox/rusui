package env

import (
	"fmt"
	"io"
	"os"
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
	WriteFile(id, rel string, data []byte) error
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
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > max {
		return fmt.Errorf("env: oversized")
	}
	if w, ok := c.RT.(workspaceFileRuntime); ok {
		return w.WriteFile(handle, clean, data)
	}
	d, ok := c.RT.(DockerCLI)
	if !ok {
		return fmt.Errorf("env: runtime cannot write files")
	}
	return d.WriteFile(handle, clean, data)
}

func (d DockerCLI) WriteFile(id, rel string, data []byte) error {
	tmp, err := os.CreateTemp("", "rusui-upload-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	dest := id + ":" + workspacePath(rel)
	_, err = d.run("cp", name, dest)
	return err
}

func (f *FakeRuntime) WriteFile(id, rel string, data []byte) error {
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
	f.Contents[id][rel] = append([]byte(nil), data...)
	f.Files[id][rel] = true
	return nil
}

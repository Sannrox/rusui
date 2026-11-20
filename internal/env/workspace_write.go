package env

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const WorkspaceUploadCap = 32 << 20

type WorkspaceWriter interface {
	WriteFile(handle, rel string, r io.Reader, max int64) error
}

type WorkspaceReader interface {
	ReadFile(handle, rel string, max int64) ([]byte, error)
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
	f, err := createUnder(handle, clean)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if max <= 0 {
		max = WorkspaceUploadCap
	}
	n, err := io.Copy(f, io.LimitReader(r, max+1))
	if err != nil {
		return err
	}
	if n > max {
		_ = f.Truncate(0)
		return fmt.Errorf("env: oversized")
	}
	return nil
}

// ReadFile returns one workspace-relative regular file of at most max
// bytes. It walks with O_NOFOLLOW and refuses a path that escapes the
// handle.
func (p Process) ReadFile(handle, rel string, max int64) ([]byte, error) {
	if handle == "" {
		return nil, fmt.Errorf("env: workspace missing")
	}
	clean, ok := SafeWorkspaceRel(rel)
	if !ok {
		return nil, ErrWorkspacePath
	}
	if max <= 0 {
		max = WorkspaceUploadCap
	}
	dirfd, err := unix.Open(handle, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, mapUnixReadErr(err)
	}
	parts := strings.Split(clean, "/")
	for i, part := range parts {
		if part == "" || part == "." {
			continue
		}
		var st unix.Stat_t
		if err := unix.Fstatat(dirfd, part, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			_ = unix.Close(dirfd)
			return nil, mapUnixReadErr(err)
		}
		if st.Mode&unix.S_IFMT == unix.S_IFLNK {
			_ = unix.Close(dirfd)
			return nil, ErrWorkspacePath
		}
		last := i == len(parts)-1
		if !last {
			if st.Mode&unix.S_IFMT != unix.S_IFDIR {
				_ = unix.Close(dirfd)
				return nil, ErrWorkspaceMissing
			}
			next, err := unix.Openat(dirfd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			_ = unix.Close(dirfd)
			if err != nil {
				return nil, mapUnixReadErr(err)
			}
			dirfd = next
			continue
		}
		if st.Mode&unix.S_IFMT == unix.S_IFDIR {
			_ = unix.Close(dirfd)
			return nil, ErrWorkspaceNotFile
		}
		if st.Mode&unix.S_IFMT != unix.S_IFREG {
			_ = unix.Close(dirfd)
			return nil, ErrWorkspacePath
		}
		if st.Size > max {
			_ = unix.Close(dirfd)
			return nil, ErrWorkspaceOversized
		}
		fd, err := unix.Openat(dirfd, part, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		_ = unix.Close(dirfd)
		if err != nil {
			return nil, mapUnixReadErr(err)
		}
		f := os.NewFile(uintptr(fd), rel)
		defer func() { _ = f.Close() }()
		var buf bytes.Buffer
		n, err := io.Copy(&buf, io.LimitReader(f, max+1))
		if err != nil {
			return nil, err
		}
		if n > max {
			return nil, ErrWorkspaceOversized
		}
		return buf.Bytes(), nil
	}
	_ = unix.Close(dirfd)
	return nil, ErrWorkspacePath
}

func mapUnixReadErr(err error) error {
	switch {
	case errors.Is(err, unix.ENOENT):
		return ErrWorkspaceMissing
	case errors.Is(err, unix.ELOOP):
		return ErrWorkspacePath
	case errors.Is(err, unix.ENOTDIR):
		return ErrWorkspaceMissing
	default:
		return fmt.Errorf("env: read workspace: %w", err)
	}
}

func createUnder(root, rel string) (*os.File, error) {
	dirfd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		if part == "" || part == "." {
			continue
		}
		last := i == len(parts)-1
		if !last {
			if err := unix.Mkdirat(dirfd, part, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
				_ = unix.Close(dirfd)
				return nil, err
			}
			next, err := unix.Openat(dirfd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			_ = unix.Close(dirfd)
			if err != nil {
				return nil, err
			}
			dirfd = next
			continue
		}
		fd, err := unix.Openat(dirfd, part, unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		_ = unix.Close(dirfd)
		if err != nil {
			return nil, err
		}
		return os.NewFile(uintptr(fd), rel), nil
	}
	_ = unix.Close(dirfd)
	return nil, fmt.Errorf("env: path")
}

type workspaceFileRuntime interface {
	WriteFile(id, rel string, r io.Reader, max int64) error
}

type workspaceReadRuntime interface {
	ReadWorkspace(id, rel string, max int64) ([]byte, error)
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

// ReadFile returns one workspace-relative regular file of at most max
// bytes from a container runtime.
func (c Container) ReadFile(handle, rel string, max int64) ([]byte, error) {
	if handle == "" {
		return nil, fmt.Errorf("env: empty handle")
	}
	clean, ok := SafeWorkspaceRel(rel)
	if !ok {
		return nil, ErrWorkspacePath
	}
	if max <= 0 {
		max = WorkspaceUploadCap
	}
	if w, ok := c.RT.(workspaceReadRuntime); ok {
		return w.ReadWorkspace(handle, clean, max)
	}
	d, ok := c.RT.(DockerCLI)
	if !ok {
		return nil, fmt.Errorf("env: runtime cannot read files")
	}
	return d.ReadWorkspace(handle, clean, max)
}

func mapReadExit(err error) error {
	if mapped := mapListExit(err); mapped != nil {
		if errors.Is(mapped, ErrWorkspaceNotDir) {
			return ErrWorkspaceNotFile
		}
		return mapped
	}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok && ee.ExitCode() == 12 {
		return ErrWorkspaceOversized
	}
	return nil
}

// ReadWorkspace reads one nested workspace file through the inspect
// script: no symlink components, replaced /workspace is refused, and
// the body is capped at max.
func (d DockerCLI) ReadWorkspace(id, rel string, max int64) ([]byte, error) {
	clean, err := workspaceListRel(rel)
	if err != nil {
		return nil, err
	}
	if clean == "" {
		return nil, ErrWorkspaceNotFile
	}
	if max <= 0 {
		max = WorkspaceUploadCap
	}
	out, err := exec.Command(d.bin(), "exec", id, "sh", "-c", readWorkspaceScript, "sh", clean, strconv.FormatInt(max, 10)).Output()
	if mapped := mapReadExit(err); mapped != nil {
		return nil, mapped
	}
	if err != nil {
		return nil, fmt.Errorf("env: read workspace: %w", err)
	}
	if int64(len(out)) > max {
		return nil, ErrWorkspaceOversized
	}
	return out, nil
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

func (f *FakeRuntime) ReadWorkspace(id, rel string, max int64) ([]byte, error) {
	if max <= 0 {
		max = WorkspaceUploadCap
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Replaced[id] {
		return nil, ErrWorkspaceReplaced
	}
	b, ok := f.Contents[id][rel]
	if !ok {
		prefix := rel + "/"
		for name := range f.Contents[id] {
			if strings.HasPrefix(name, prefix) {
				return nil, ErrWorkspaceNotFile
			}
		}
		return nil, ErrWorkspaceMissing
	}
	if int64(len(b)) > max {
		return nil, ErrWorkspaceOversized
	}
	return append([]byte(nil), b...), nil
}

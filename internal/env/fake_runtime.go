package env

import (
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// FakeRuntime records Docker-shaped calls. No daemon is required.
type FakeRuntime struct {
	mu              sync.Mutex
	next            int
	DefaultFiles    map[string]bool
	DefaultContents map[string][]byte
	Files           map[string]map[string]bool
	Contents        map[string]map[string][]byte
	Created         []Spec
	Stopped         []string
	Started         []string
	Removed         []string
	Execs           [][]string
	Stdio           []StdioCall
	// Snapshots and FileReads count WorkspaceSnapshot and WorkspaceFile calls.
	Snapshots int
	FileReads int
	// Links are guest-link starts (ADR 0047). The fake guest side binds
	// nothing and reports ready, so a turn's link starts and stays up.
	Links      []StdioCall
	StdioHook  func(handle string, argv, env []string) (io.WriteCloser, io.ReadCloser, func(), error)
	ExecHook   func(id string, cmd []string) error
	OutputHook func(id string, cmd []string) []byte
	StartHook  func(id string) error
	StopHook   func(id string) error
	GuestAddrs map[string]string
	alive      map[string]bool
	// secrets are outside Contents so workspace snapshot, read, and
	// CaptureTree never observe injected values.
	secrets map[string]map[string]string
}

type StdioCall struct {
	Handle string
	Argv   []string
	Env    []string
}

func (f *FakeRuntime) CreateAndStart(spec Spec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	id := fmt.Sprintf("ctr-%d", f.next)
	f.Created = append(f.Created, spec)
	if f.alive == nil {
		f.alive = map[string]bool{}
	}
	f.alive[id] = true
	if len(f.DefaultFiles) > 0 {
		if f.Files == nil {
			f.Files = map[string]map[string]bool{}
		}
		f.Files[id] = maps.Clone(f.DefaultFiles)
	}
	if len(f.DefaultContents) > 0 {
		if f.Contents == nil {
			f.Contents = map[string]map[string][]byte{}
		}
		f.Contents[id] = maps.Clone(f.DefaultContents)
		if f.Files == nil {
			f.Files = map[string]map[string]bool{}
		}
		if f.Files[id] == nil {
			f.Files[id] = map[string]bool{}
		}
		for path := range f.DefaultContents {
			f.Files[id][path] = true
		}
	}
	return id, nil
}

func (f *FakeRuntime) Stop(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Stopped = append(f.Stopped, id)
	if f.StopHook != nil {
		if err := f.StopHook(id); err != nil {
			return err
		}
	}
	if f.alive != nil {
		f.alive[id] = false
	}
	return nil
}

func (f *FakeRuntime) Start(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Started = append(f.Started, id)
	if f.StartHook != nil {
		if err := f.StartHook(id); err != nil {
			return err
		}
	}
	if f.alive == nil {
		f.alive = map[string]bool{}
	}
	f.alive[id] = true
	return nil
}

func (f *FakeRuntime) Remove(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Removed = append(f.Removed, id)
	delete(f.alive, id)
	return nil
}

func (f *FakeRuntime) HasFile(id, path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Contents != nil {
		if _, ok := f.Contents[id][path]; ok {
			return true
		}
	}
	if f.Files == nil {
		return false
	}
	return f.Files[id][path]
}

func (f *FakeRuntime) ReadFile(id, path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Contents != nil {
		if b, ok := f.Contents[id][path]; ok {
			return b, nil
		}
	}
	return nil, os.ErrNotExist
}

func (f *FakeRuntime) Exec(id string, cmd []string) error {
	f.mu.Lock()
	copied := append([]string{id}, cmd...)
	f.Execs = append(f.Execs, copied)
	hook := f.ExecHook
	f.mu.Unlock()
	if hook != nil {
		return hook(id, cmd)
	}
	return nil
}

// ExecOutput records the exec like Exec and returns OutputHook's bytes,
// bounded the way DockerCLI bounds them.
func (f *FakeRuntime) ExecOutput(id string, cmd []string) ([]byte, bool, error) {
	err := f.Exec(id, cmd)
	f.mu.Lock()
	hook := f.OutputHook
	f.mu.Unlock()
	buf := &tailBuffer{limit: CaptureLimit}
	if hook != nil {
		_, _ = buf.Write(hook(id, cmd))
	}
	out, truncated := buf.result()
	return out, truncated, err
}

func (f *FakeRuntime) GuestAddr(id string, port int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := validGuestPort(port); err != nil {
		return "", err
	}
	if f.GuestAddrs == nil {
		return "", fmt.Errorf("env: no guest address")
	}
	addr := f.GuestAddrs[id+"/"+strconv.Itoa(port)]
	if addr == "" {
		addr = f.GuestAddrs[id]
	}
	if addr == "" {
		return "", fmt.Errorf("env: no guest address")
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return "", err
	}
	return addr, nil
}

func (f *FakeRuntime) ExecStdio(handle string, argv, env []string) (io.WriteCloser, io.ReadCloser, func(), error) {
	if IsLinkArgv(argv) {
		f.mu.Lock()
		f.Links = append(f.Links, StdioCall{Handle: handle, Argv: append([]string(nil), argv...)})
		f.mu.Unlock()
		return fakeLinkGuest()
	}
	f.mu.Lock()
	f.Stdio = append(f.Stdio, StdioCall{Handle: handle, Argv: append([]string(nil), argv...), Env: append([]string(nil), env...)})
	hook := f.StdioHook
	f.mu.Unlock()
	if hook == nil {
		return nil, nil, nil, fmt.Errorf("env: stdio hook required")
	}
	return hook(handle, argv, env)
}

func (f *FakeRuntime) PutSecret(handle, id, value string) error {
	if !validSecretFileID(id) {
		return fmt.Errorf("env: invalid secret id")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.secrets == nil {
		f.secrets = map[string]map[string]string{}
	}
	if f.secrets[handle] == nil {
		f.secrets[handle] = map[string]string{}
	}
	f.secrets[handle][id] = value
	return nil
}

func (f *FakeRuntime) DeleteSecrets(handle string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.secrets, handle)
	return nil
}

// LookupSecret is a test inspection of values that are not in Contents.
func (f *FakeRuntime) LookupSecret(handle, id string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.secrets[handle][id]
	return v, ok
}

func (f *FakeRuntime) SetFile(id, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Files == nil {
		f.Files = map[string]map[string]bool{}
	}
	if f.Files[id] == nil {
		f.Files[id] = map[string]bool{}
	}
	f.Files[id][path] = true
}

// RemoveFile deletes a file from the fake container.
func (f *FakeRuntime) RemoveFile(id, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.Files[id], path)
	delete(f.Contents[id], path)
}

func (f *FakeRuntime) PlaceTree(id, srcDir string) error {
	return filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == ".rusui-snapshot" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f.SetFileContent(id, rel, data)
		return nil
	})
}

// CaptureTree writes the fake workspace into destDir. A path known only
// through Files is written empty.
func (f *FakeRuntime) CaptureTree(id, destDir string) error {
	f.mu.Lock()
	files := map[string][]byte{}
	for path, ok := range f.Files[id] {
		if ok {
			files[path] = nil
		}
	}
	for path, data := range f.Contents[id] {
		files[path] = append([]byte(nil), data...)
	}
	f.mu.Unlock()
	for path, data := range files {
		if !filepath.IsLocal(path) {
			continue
		}
		out := filepath.Join(destDir, path)
		if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(out, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (f *FakeRuntime) SetFileContent(id, path string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Files == nil {
		f.Files = map[string]map[string]bool{}
	}
	if f.Files[id] == nil {
		f.Files[id] = map[string]bool{}
	}
	f.Files[id][path] = true
	if f.Contents == nil {
		f.Contents = map[string]map[string][]byte{}
	}
	if f.Contents[id] == nil {
		f.Contents[id] = map[string][]byte{}
	}
	f.Contents[id][path] = data
}

// WorkspaceNames lists the fake container's top-level contents.
func (f *FakeRuntime) WorkspaceNames(id string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for name := range f.Contents[id] {
		if !strings.Contains(name, "/") {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out, nil
}

// WorkspaceFile returns a top-level fake content under the size cap.
func (f *FakeRuntime) WorkspaceFile(id, name string, limit int64) ([]byte, string, error) {
	f.mu.Lock()
	f.FileReads++
	f.mu.Unlock()
	if strings.ContainsAny(name, "/\\") {
		return nil, FilePathDenied, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.Contents[id][name]
	if !ok {
		return nil, FileMissing, nil
	}
	if int64(len(b)) > limit {
		return nil, FileOversized, nil
	}
	return append([]byte(nil), b...), "", nil
}

// WorkspaceSnapshot returns the fake container's top-level contents in
// one call, with the same caps as the real runtime.
func (f *FakeRuntime) WorkspaceSnapshot(id string, fileCap, totalCap int64) ([]WorkspaceEntry, error) {
	names, _ := f.WorkspaceNames(id)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Snapshots++
	var out []WorkspaceEntry
	var total int64
	for _, n := range names {
		b := f.Contents[id][n]
		switch {
		case int64(len(b)) > fileCap:
			out = append(out, WorkspaceEntry{Name: n, State: FileOversized})
		case total >= totalCap:
			out = append(out, WorkspaceEntry{Name: n, State: FileSkipped})
		default:
			total += int64(len(b))
			out = append(out, WorkspaceEntry{Name: n, Body: append([]byte(nil), b...)})
		}
	}
	return out, nil
}

// fakeLinkGuest is a guest-link guest side with no listeners: it reports
// ready and then holds the link open until the host closes it.
func fakeLinkGuest() (io.WriteCloser, io.ReadCloser, func(), error) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() {
		_, _ = outW.Write([]byte{linkReady, 0, 0, 0, 0, 0, 0, 0, 0})
		_, _ = io.Copy(io.Discard, inR)
		_ = outW.Close()
	}()
	stop := func() {
		_ = inW.Close()
		_ = outW.Close()
	}
	return inW, outR, stop, nil
}

package env

import (
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
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
	StdioHook       func(handle string, argv, env []string) (io.WriteCloser, io.ReadCloser, func(), error)
	ExecHook        func(id string, cmd []string) error
	StartHook       func(id string) error
	StopHook        func(id string) error
	alive           map[string]bool
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

func (f *FakeRuntime) ExecStdio(handle string, argv, env []string) (io.WriteCloser, io.ReadCloser, func(), error) {
	f.mu.Lock()
	f.Stdio = append(f.Stdio, StdioCall{Handle: handle, Argv: append([]string(nil), argv...), Env: append([]string(nil), env...)})
	hook := f.StdioHook
	f.mu.Unlock()
	if hook == nil {
		return nil, nil, nil, fmt.Errorf("env: stdio hook required")
	}
	return hook(handle, argv, env)
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

package env

import (
	"fmt"
	"maps"
	"sync"
)

// FakeRuntime records Docker-shaped calls. No daemon is required.
type FakeRuntime struct {
	mu           sync.Mutex
	next         int
	DefaultFiles map[string]bool
	Files        map[string]map[string]bool
	Created      []Spec
	Stopped      []string
	Started      []string
	Removed      []string
	Execs        [][]string
	alive        map[string]bool
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
	return id, nil
}

func (f *FakeRuntime) Stop(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Stopped = append(f.Stopped, id)
	if f.alive != nil {
		f.alive[id] = false
	}
	return nil
}

func (f *FakeRuntime) Start(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Started = append(f.Started, id)
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
	if f.Files == nil {
		return false
	}
	return f.Files[id][path]
}

func (f *FakeRuntime) Exec(id string, cmd []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := append([]string{id}, cmd...)
	f.Execs = append(f.Execs, copied)
	return nil
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

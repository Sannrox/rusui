package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

// setupBox is a container engine whose `.agents/setup` writes built.txt
// into the workspace from the pin's README (#512).
type setupBox struct {
	h      *harn
	rt     *env.FakeRuntime
	tree   *engine.MemoryTree
	root   string
	mu     sync.Mutex
	setups int
	fail   bool
}

func newSetupBox(t *testing.T) *setupBox {
	b := &setupBox{h: setup(t), root: t.TempDir()}
	b.tree = &engine.MemoryTree{Files: map[string][]byte{
		"README":      []byte("pin-a\n"),
		env.SetupPath: []byte("#!/bin/sh\necho built > built.txt\n"),
	}}
	b.rt = &env.FakeRuntime{}
	b.rt.ExecHook = func(id string, cmd []string) error {
		if len(cmd) < 2 || cmd[1] != env.SetupPath {
			return nil
		}
		b.mu.Lock()
		b.setups++
		fail := b.fail
		b.mu.Unlock()
		readme, err := b.rt.ReadFile(id, "README")
		if err != nil {
			return err
		}
		b.rt.SetFileContent(id, "out/built.txt", append([]byte("built from "), readme...))
		if fail {
			return fmt.Errorf("exit status 1")
		}
		return nil
	}
	b.h.e.Container = env.Container{RT: b.rt, Image: "rusui-guest:test"}
	b.h.e.SnapshotRoot = b.root
	b.h.e.Tree = b.tree
	return b
}

func (b *setupBox) provision(t *testing.T, name, pin string) (*store.Environment, error) {
	t.Helper()
	return b.h.e.ProvisionEnvironment(engine.EnvSpec{
		Name: name, Kind: env.KindContainer, Repo: "example/test-repo", Pin: pin,
		SourceHash: engine.SourceHash("rusui-guest:test", pin, nil),
	})
}

func (b *setupBox) mustProvision(t *testing.T, name, pin string) *store.Environment {
	t.Helper()
	got, err := b.provision(t, name, pin)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (b *setupBox) setupRuns() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.setups
}

func (b *setupBox) wantBuilt(t *testing.T, handle, want string) {
	t.Helper()
	got, err := b.rt.ReadFile(handle, "out/built.txt")
	if err != nil || string(got) != want {
		t.Fatalf("built.txt in %s = %q %v, want %q", handle, got, err, want)
	}
}

func TestSecondEnvironmentGetsSetupWritesWithoutSetup(t *testing.T) {
	b := newSetupBox(t)
	first := b.mustProvision(t, "box-1", "aaa")
	b.wantBuilt(t, first.Handle, "built from pin-a\n")
	second := b.mustProvision(t, "box-2", "aaa")
	b.wantBuilt(t, second.Handle, "built from pin-a\n")
	if n := b.setupRuns(); n != 1 {
		t.Fatalf("setup ran %d times, want once per source hash", n)
	}
	if b.tree.Calls != 1 {
		t.Fatalf("pin fetched %d times", b.tree.Calls)
	}
}

func TestEnvironmentAfterTTLStillReusesSetupWrites(t *testing.T) {
	b := newSetupBox(t)
	first := b.mustProvision(t, "box-1", "aaa")
	b.h.clk.T = first.ExpiresAt.Add(time.Minute)
	if err := b.h.e.ReapEnvironments(); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetEnvironment(b.h.st, first.ID); err != nil || got.State != store.EnvReady || got.Handle != first.Handle {
		t.Fatalf("first env %+v %v", got, err)
	}
	later := b.mustProvision(t, "box-2", "aaa")
	b.wantBuilt(t, later.Handle, "built from pin-a\n")
	if n := b.setupRuns(); n != 1 {
		t.Fatalf("setup ran %d times after ttl, want 1", n)
	}
}

func TestFailedSetupLeavesNoSnapshot(t *testing.T) {
	b := newSetupBox(t)
	b.fail = true
	if _, err := b.provision(t, "box-1", "aaa"); err == nil {
		t.Fatal("expected setup failure")
	}
	hash := engine.SourceHash("rusui-guest:test", "aaa", nil)
	if _, err := os.Stat(filepath.Join(b.root, hash)); !os.IsNotExist(err) {
		t.Fatalf("failed setup left a snapshot: %v", err)
	}
	if entries, err := os.ReadDir(b.root); err != nil || len(entries) != 0 {
		t.Fatalf("snapshot root after failure %v %v", entries, err)
	}
	b.fail = false
	retry := b.mustProvision(t, "box-2", "aaa")
	if n := b.setupRuns(); n != 2 {
		t.Fatalf("setup ran %d times, want a rerun after failure", n)
	}
	b.wantBuilt(t, retry.Handle, "built from pin-a\n")
	third := b.mustProvision(t, "box-3", "aaa")
	b.wantBuilt(t, third.Handle, "built from pin-a\n")
	if n := b.setupRuns(); n != 2 {
		t.Fatalf("setup ran %d times after a stored snapshot", n)
	}
}

func TestChangedPinRunsSetupAndStoresNewTree(t *testing.T) {
	b := newSetupBox(t)
	b.mustProvision(t, "box-a", "aaa")
	b.tree.Files["README"] = []byte("pin-b\n")
	onB := b.mustProvision(t, "box-b", "bbb")
	b.wantBuilt(t, onB.Handle, "built from pin-b\n")
	if n := b.setupRuns(); n != 2 {
		t.Fatalf("setup ran %d times, want once per pin", n)
	}
	againB := b.mustProvision(t, "box-b2", "bbb")
	b.wantBuilt(t, againB.Handle, "built from pin-b\n")
	againA := b.mustProvision(t, "box-a2", "aaa")
	b.wantBuilt(t, againA.Handle, "built from pin-a\n")
	if n := b.setupRuns(); n != 2 {
		t.Fatalf("setup ran %d times, want no rerun for stored pins", n)
	}
}

func TestPrepareRunsOnlyRepositorySetup(t *testing.T) {
	b := newSetupBox(t)
	b.mustProvision(t, "box-1", "aaa")
	if n := b.setupRuns(); n != 1 {
		t.Fatalf("setup ran %d times", n)
	}
	for _, cmd := range b.rt.Execs {
		joined := strings.Join(cmd, " ")
		if strings.Contains(joined, "pre-clone") || strings.Contains(joined, "pre-setup") {
			t.Fatalf("plane hook %q", joined)
		}
	}
}

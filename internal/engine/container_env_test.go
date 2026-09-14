package engine_test

import (
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

func TestContainerDriverLifecycleAndScripts(t *testing.T) {
	h := setup(t)
	rt := &env.FakeRuntime{DefaultFiles: map[string]bool{
		env.SetupPath:  true,
		env.ResumePath: true,
	}}
	h.e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}

	created, err := h.e.ProvisionEnvironment(engine.EnvSpec{
		Name: "box-1", Kind: env.KindContainer, SourceHash: "src-a",
		CPUMillis: 500, MemoryBytes: 256 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Driver != env.KindContainer || created.SourceHash != "src-a" {
		t.Fatalf("created %+v", created)
	}
	if created.CPUMillis != 500 || created.MemoryBytes != 256<<20 {
		t.Fatalf("sizes %+v", created)
	}
	if len(rt.Created) != 1 || rt.Created[0].Image != "rusui-guest:test" || rt.Created[0].CPUMillis != 500 {
		t.Fatalf("runtime create %#v", rt.Created)
	}
	if len(rt.Execs) != 1 || rt.Execs[0][1] != "/bin/sh" || rt.Execs[0][2] != env.SetupPath {
		t.Fatalf("setup execs %#v", rt.Execs)
	}

	if _, err := h.e.SleepEnvironment(created.ID); err != nil {
		t.Fatal(err)
	}
	if len(rt.Stopped) != 1 || rt.Stopped[0] != created.Handle {
		t.Fatalf("stop %#v", rt.Stopped)
	}

	if _, err := h.e.WakeEnvironment(created.ID); err != nil {
		t.Fatal(err)
	}
	if len(rt.Started) != 1 || rt.Started[0] != created.Handle {
		t.Fatalf("start %#v", rt.Started)
	}
	if len(rt.Execs) != 2 || rt.Execs[1][2] != env.ResumePath {
		t.Fatalf("resume execs %#v", rt.Execs)
	}

	// Same source hash on a new environment still sets up (new handle).
	// Wake must not re-run setup on the first handle.
	setups := 0
	for _, ex := range rt.Execs {
		if len(ex) > 2 && ex[2] == env.SetupPath {
			setups++
		}
	}
	if setups != 1 {
		t.Fatalf("setup ran %d times, want 1 per environment source hash", setups)
	}

	got, err := store.GetEnvironment(h.st, created.ID)
	if err != nil || got.State != store.EnvReady {
		t.Fatalf("after wake %+v %v", got, err)
	}
}

func TestContainerDriverSkippedWithoutRuntime(t *testing.T) {
	h := setup(t)
	if _, err := h.e.ProvisionEnvironment(engine.EnvSpec{Name: "box", Kind: env.KindContainer}); err == nil {
		t.Fatal("expected error when container driver is unset")
	}
}

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
		t.Fatalf("setup ran %d times before second env, got %d", 1, setups)
	}
	if _, err := h.e.ProvisionEnvironment(engine.EnvSpec{
		Name: "box-2", Kind: env.KindContainer, SourceHash: "src-a",
	}); err != nil {
		t.Fatal(err)
	}
	setups = 0
	for _, ex := range rt.Execs {
		if len(ex) > 2 && ex[2] == env.SetupPath {
			setups++
		}
	}
	if setups != 1 {
		t.Fatalf("setup ran %d times, want once per source hash", setups)
	}

	got, err := store.GetEnvironment(h.st, created.ID)
	if err != nil || got.State != store.EnvReady {
		t.Fatalf("after wake %+v %v", got, err)
	}
}

func TestContainerWakeStartsServices(t *testing.T) {
	h := setup(t)
	rt := &env.FakeRuntime{DefaultContents: map[string][]byte{
		env.ServicesRusuiPath: []byte("services:\n  web:\n    command: pnpm dev\n"),
	}}
	h.e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	created, err := h.e.ProvisionEnvironment(engine.EnvSpec{Name: "box-svc", Kind: env.KindContainer})
	if err != nil {
		t.Fatal(err)
	}
	if len(rt.Execs) != 1 || rt.Execs[0][len(rt.Execs[0])-1] != "pnpm dev" {
		t.Fatalf("create execs %#v", rt.Execs)
	}
	if _, err := h.e.SleepEnvironment(created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.WakeEnvironment(created.ID); err != nil {
		t.Fatal(err)
	}
	if len(rt.Execs) != 2 || rt.Execs[1][len(rt.Execs[1])-1] != "pnpm dev" {
		t.Fatalf("wake execs %#v", rt.Execs)
	}
}

func TestClaimProvisionsSessionEnvironment(t *testing.T) {
	h := setup(t)
	rt := &env.FakeRuntime{DefaultFiles: map[string]bool{env.SetupPath: true}}
	h.e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	h.putRefresh(issue(1))
	c := h.claim()
	turn, err := store.GetTurn(h.st, c.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, turn.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.GetEnvironment(h.st, sess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Handle == "" || got.SourceHash != engine.SourceHash("rusui-guest:test", "aaa", nil) {
		t.Fatalf("env %+v", got)
	}
	it := issue(1)
	it.MainSHA = "bbb"
	if err := h.e.EnsureSessionEnvironment(c.Job.ID, it); err != nil {
		t.Fatal(err)
	}
	sess2, err := store.GetSession(h.st, turn.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if sess2.EnvironmentID == sess.EnvironmentID {
		t.Fatal("pin change kept environment")
	}
}

func TestContainerDriverSkippedWithoutRuntime(t *testing.T) {
	h := setup(t)
	if _, err := h.e.ProvisionEnvironment(engine.EnvSpec{Name: "box", Kind: env.KindContainer}); err == nil {
		t.Fatal("expected error when container driver is unset")
	}
}

package engine_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

func TestEnvironmentCreateSleepWakeExpire(t *testing.T) {
	h := setup(t)
	root := t.TempDir()
	h.e.Env = env.Process{Root: root}

	created, err := h.e.CreateEnvironment("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if created.State != store.EnvReady || created.Driver != env.KindProcess || created.Handle == "" {
		t.Fatalf("create: %+v", created)
	}
	if created.CPUMillis != 0 || created.MemoryBytes != 0 {
		t.Fatalf("default size cpu=%d mem=%d", created.CPUMillis, created.MemoryBytes)
	}
	if _, err := os.Stat(filepath.Join(created.Handle, ".rusui-env")); err != nil {
		t.Fatal(err)
	}
	if store.EnvTTL != 72*time.Hour {
		t.Fatalf("EnvTTL %s, want 72h", store.EnvTTL)
	}
	if created.ExpiresAt == nil || !created.ExpiresAt.Equal(h.clk.T.Add(store.EnvTTL)) {
		t.Fatalf("ttl %+v", created.ExpiresAt)
	}

	slept, err := h.e.SleepEnvironment(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if slept.State != store.EnvSleeping || slept.SleptAt == nil {
		t.Fatalf("sleep: %+v", slept)
	}
	b, err := os.ReadFile(filepath.Join(created.Handle, ".rusui-env"))
	if err != nil || string(b) != "sleeping\n" {
		t.Fatalf("sleep file %q %v", b, err)
	}

	h.clk.Advance(time.Hour)
	woke, err := h.e.WakeEnvironment(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if woke.State != store.EnvReady || woke.SleptAt != nil {
		t.Fatalf("wake: %+v", woke)
	}

	h.clk.T = woke.ExpiresAt.Add(-time.Second)
	if err := h.e.ReapEnvironments(); err != nil {
		t.Fatal(err)
	}
	live, err := store.GetEnvironment(h.st, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if live.State != store.EnvReady {
		t.Fatalf("reap before ttl: %s", live.State)
	}
	if _, err := os.Stat(created.Handle); err != nil {
		t.Fatalf("handle gone before ttl: %v", err)
	}

	h.clk.T = woke.ExpiresAt.Add(time.Minute)
	if err := h.e.ReapEnvironments(); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetEnvironment(h.st, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != store.EnvReady || got.Handle != created.Handle {
		t.Fatalf("reap after ttl %+v", got)
	}
	if _, err := os.Stat(created.Handle); err != nil {
		t.Fatalf("handle gone after ttl: %v", err)
	}

	local, err := store.GetEnvironment(h.st, store.DefaultEnvironmentID)
	if err != nil || local.Name != store.LocalEnvironmentName || local.State != store.EnvReady {
		t.Fatalf("local %+v %v", local, err)
	}
}

func TestRecoverKeepsEnvironmentPastTTL(t *testing.T) {
	h := setup(t)
	h.e.Env = env.Process{Root: t.TempDir()}
	created, err := h.e.CreateEnvironment("ws-reap")
	if err != nil {
		t.Fatal(err)
	}
	h.clk.T = created.ExpiresAt.Add(time.Second)
	if err := h.e.Recover(); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetEnvironment(h.st, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != store.EnvReady || got.Handle != created.Handle {
		t.Fatalf("recover state %+v", got)
	}
}

func TestWakeRestartsServicesYAML(t *testing.T) {
	h := setup(t)
	h.e.Env = env.Process{Root: t.TempDir()}
	created, err := h.e.CreateEnvironment("ws-svc")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(created.Handle, ".rusui"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(created.Handle, env.ServicesRusuiPath), []byte("services:\n  sleeper:\n    command: sleep 30\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.SleepEnvironment(created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.WakeEnvironment(created.ID); err != nil {
		t.Fatal(err)
	}
	pidb, err := os.ReadFile(filepath.Join(created.Handle, ".rusui", "svc", "sleeper.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidb)))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("not running after wake: %v", err)
	}
	if _, err := h.e.SleepEnvironment(created.ID); err != nil {
		t.Fatal(err)
	}
	// SIGKILL is delivered asynchronously and the service stays a zombie until
	// its waiter reaps it, so allow the exit a moment to land.
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatal("still running after sleep")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

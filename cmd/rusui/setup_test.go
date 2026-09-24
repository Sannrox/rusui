package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	envpkg "github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/setup"
)

func TestSetupCLIPlanApplyNeverPrintsSecrets(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	noRuntime := func() (envpkg.Runtime, error) { return nil, fmt.Errorf("none") }
	getenv := func(string) string { return "" }

	var plan bytes.Buffer
	if code := setupMain([]string{"plan", "-state", dir}, &plan, getenv, noRuntime, nil); code != 0 {
		t.Fatalf("plan exit %d\n%s", code, plan.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("plan wrote the state directory")
	}

	var apply bytes.Buffer
	code := setupMain([]string{"apply", "-state", dir}, &apply, getenv, noRuntime, nil)
	out := apply.String()
	for k, v := range setup.ReadEnv(setup.PathsFor(dir).Env) {
		if len(v) >= 16 && strings.Contains(out, v) {
			t.Fatalf("%s value printed", k)
		}
	}
	if !strings.Contains(out, "needs-you") || !strings.Contains(out, "diagnose ready=") || !strings.Contains(out, "start: set -a;") {
		t.Fatalf("apply output:\n%s", out)
	}
	if code == 2 {
		t.Fatalf("apply usage error:\n%s", out)
	}
	if code := setupMain([]string{"bogus"}, &bytes.Buffer{}, getenv, noRuntime, nil); code != 2 {
		t.Fatalf("bogus mode exit %d", code)
	}
}

type cliServiceManager struct {
	removed int
}

func (*cliServiceManager) Plan(setup.Options, setup.Paths) (setup.Step, error) {
	return setup.Step{Action: setup.Keep, Item: "user service"}, nil
}

func (*cliServiceManager) Apply(setup.Options, setup.Paths) (setup.Step, error) {
	return setup.Step{Action: setup.Keep, Item: "user service"}, nil
}

func (m *cliServiceManager) Remove(setup.Options) (setup.Step, error) {
	m.removed++
	return setup.Step{Action: setup.Remove, Item: "user service", Detail: "removed"}, nil
}

func TestSetupRemoveServiceSkipsRuntimeProbe(t *testing.T) {
	manager := &cliServiceManager{}
	look := func() (envpkg.Runtime, error) {
		t.Fatal("remove-service should not inspect container runtime")
		return nil, nil
	}
	var out bytes.Buffer
	code := setupMain([]string{"remove-service", "-addr", "127.0.0.1:18080", "-json"}, &out, func(string) string { return t.TempDir() }, look, manager)
	if code != 0 || manager.removed != 1 || !strings.Contains(out.String(), `"action":"remove"`) {
		t.Fatalf("remove-service code=%d calls=%d output=%s", code, manager.removed, out.String())
	}
}

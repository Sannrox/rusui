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
	if code := setupMain([]string{"plan", "-state", dir}, &plan, getenv, noRuntime); code != 0 {
		t.Fatalf("plan exit %d\n%s", code, plan.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("plan wrote the state directory")
	}

	var apply bytes.Buffer
	code := setupMain([]string{"apply", "-state", dir}, &apply, getenv, noRuntime)
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
	if code := setupMain([]string{"bogus"}, &bytes.Buffer{}, getenv, noRuntime); code != 2 {
		t.Fatalf("bogus mode exit %d", code)
	}
}

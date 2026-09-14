package env

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestParseServicesAmpShape(t *testing.T) {
	svcs, err := ParseServices([]byte(`
services:
  web:
    command: pnpm dev -- --host 0.0.0.0 --port "$PORT"
    cwd: app
    env:
      API_MODE: development
    health: /healthz
    portal:
      url: /
`))
	if err != nil || len(svcs) != 1 {
		t.Fatalf("%v %#v", err, svcs)
	}
	if svcs[0].Name != "web" || svcs[0].Command == "" || svcs[0].Cwd != "app" || svcs[0].Env["API_MODE"] != "development" {
		t.Fatalf("%+v", svcs[0])
	}
}

func TestParseServicesRejectsEmptyAndBadName(t *testing.T) {
	if _, err := ParseServices([]byte(`services: {}`)); err == nil {
		t.Fatal("empty mapping")
	}
	if _, err := ParseServices([]byte("services:\n  Web:\n    command: true\n")); err == nil {
		t.Fatal("uppercase name")
	}
	if _, err := ParseServices([]byte("services:\n  web:\n    cwd: app\n")); err == nil {
		t.Fatal("missing command")
	}
}

func TestLoadPrefersRusuiOverAmp(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rusui"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".amp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ServicesRusuiPath), []byte("services:\n  rusui:\n    command: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ServicesAmpPath), []byte("services:\n  amp:\n    command: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svcs, err := loadServices(func(path string) ([]byte, error) {
		return readWorkspaceFile(root, path)
	})
	if err != nil || len(svcs) != 1 || svcs[0].Name != "rusui" {
		t.Fatalf("%v %#v", err, svcs)
	}
}

func TestProcessStartStopServices(t *testing.T) {
	d := Process{Root: t.TempDir()}
	handle, err := d.Create("box")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(handle, ".rusui"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(handle, ServicesRusuiPath), []byte("services:\n  sleeper:\n    command: sleep 30\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.StartServices(handle); err != nil {
		t.Fatal(err)
	}
	pidb, err := os.ReadFile(filepath.Join(handle, servicePIDDir, "sleeper.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidb)))
	if err != nil || pid <= 0 {
		t.Fatalf("pid %s", pidb)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("service not running: %v", err)
	}
	if err := d.StopServices(handle); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("service still running after stop")
}

func TestProcessStartServicesMissingFile(t *testing.T) {
	d := Process{Root: t.TempDir()}
	handle, err := d.Create("box")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.StartServices(handle); err != nil {
		t.Fatal(err)
	}
	if err := d.StopServices(handle); err != nil {
		t.Fatal(err)
	}
}

func TestContainerStartServicesExecsCommand(t *testing.T) {
	rt := &FakeRuntime{}
	c := Container{RT: rt, Image: "rusui-guest:test"}
	id, err := c.Create("box")
	if err != nil {
		t.Fatal(err)
	}
	rt.SetFileContent(id, ServicesAmpPath, []byte("services:\n  web:\n    command: pnpm dev\n"))
	if err := c.StartServices(id); err != nil {
		t.Fatal(err)
	}
	if len(rt.Execs) != 1 || !strings.Contains(strings.Join(rt.Execs[0], " "), "pnpm dev") {
		t.Fatalf("execs %#v", rt.Execs)
	}
	if err := c.StopServices(id); err != nil {
		t.Fatal(err)
	}
}

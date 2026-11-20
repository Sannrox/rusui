package env

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestParseServicesPortAndHealth(t *testing.T) {
	svcs, err := ParseServices([]byte(`
services:
  web:
    command: pnpm dev -- --host 0.0.0.0 --port "$PORT"
    cwd: app
    env:
      API_MODE: development
    port: 3000
    health: /healthz
  worker:
    command: pnpm worker
`))
	if err != nil || len(svcs) != 2 {
		t.Fatalf("%v %#v", err, svcs)
	}
	if svcs[0].Name != "web" || svcs[0].Port != 3000 || svcs[0].Health != "/healthz" || svcs[0].Cwd != "app" || svcs[0].Env["API_MODE"] != "development" {
		t.Fatalf("%+v", svcs[0])
	}
	if svcs[1].Name != "worker" || svcs[1].Port != 0 || svcs[1].Health != "" {
		t.Fatalf("%+v", svcs[1])
	}
}

func TestParseServicesRejectsUnknownKey(t *testing.T) {
	if _, err := ParseServices([]byte(`
services:
  web:
    command: pnpm dev
    portal:
      url: /
`)); err == nil || AsParseError(err) == nil {
		t.Fatal("unknown key")
	}
}

func TestParseServicesRejectsHealthURLAndHealthWithoutPort(t *testing.T) {
	if _, err := ParseServices([]byte("services:\n  web:\n    command: true\n    port: 3000\n    health: http://other-host/\n")); err == nil || AsParseError(err) == nil {
		t.Fatal("health URL")
	}
	if _, err := ParseServices([]byte("services:\n  web:\n    command: true\n    health: /healthz\n")); err == nil || AsParseError(err) == nil {
		t.Fatal("health without port")
	}
}

func TestParseServicesRejectsDuplicatePorts(t *testing.T) {
	if _, err := ParseServices([]byte("services:\n  web:\n    command: true\n    port: 3000\n  api:\n    command: true\n    port: 3000\n")); err == nil || AsParseError(err) == nil {
		t.Fatal("duplicate ports")
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

func TestLoadRequiresRusuiFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".amp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".amp", "services.yaml"), []byte("services:\n  other:\n    command: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svcs, err := loadServices(func(path string) ([]byte, error) {
		return readWorkspaceFile(root, path)
	})
	if err != nil || len(svcs) != 0 {
		t.Fatalf("foreign file must be ignored: %v %#v", err, svcs)
	}
	if err := os.MkdirAll(filepath.Join(root, ".rusui"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ServicesRusuiPath), []byte("services:\n  rusui:\n    command: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svcs, err = loadServices(func(path string) ([]byte, error) {
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
	if _, err := d.StartServices(handle, 0); err != nil {
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
	got, err := d.StartServices(handle, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Status) != 0 || len(got.Captures) != 0 {
		t.Fatalf("missing file: %+v", got)
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
	rt.SetFileContent(id, ServicesRusuiPath, []byte("services:\n  web:\n    command: pnpm dev\n"))
	if _, err := c.StartServices(id, 7); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(rt.Execs[0], " ")
	if len(rt.Execs) != 1 || !strings.Contains(joined, "pnpm dev") || !strings.Contains(joined, "RUSUI_SESSION_ID") || !strings.Contains(joined, "RUSUI_SERVICE") {
		t.Fatalf("execs %#v", rt.Execs)
	}
	if err := c.StopServices(id); err != nil {
		t.Fatal(err)
	}
}

func TestProcessStartServicesInvalidFileStartsNone(t *testing.T) {
	d := Process{Root: t.TempDir()}
	handle, err := d.Create("box")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(handle, ".rusui"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(handle, ServicesRusuiPath), []byte("services:\n  web:\n    command: sleep 30\n    prot: 3000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := d.StartServices(handle, 1)
	if AsParseError(err) == nil || len(got.Status) != 0 {
		t.Fatalf("parse error starts none: %v %+v", err, got)
	}
	if _, err := os.Stat(filepath.Join(handle, servicePIDDir, "web.pid")); !os.IsNotExist(err) {
		t.Fatal("started a service from an invalid file")
	}
}

func TestProcessInjectsPortAndIdentity(t *testing.T) {
	d := Process{Root: t.TempDir()}
	handle, err := d.Create("box")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(handle, ".rusui"), 0o700); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(handle, "env.out")
	yaml := "services:\n  web:\n    command: sh -c 'printf %s \"$PORT $RUSUI_SESSION_ID $RUSUI_SERVICE $ORIGIN\" > env.out'\n    port: 3000\n"
	if err := os.WriteFile(filepath.Join(handle, ServicesRusuiPath), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	oldDeadline, oldEvery := healthDeadline, healthProbeEvery
	healthDeadline = 80 * time.Millisecond
	healthProbeEvery = 10 * time.Millisecond
	t.Cleanup(func() {
		healthDeadline, healthProbeEvery = oldDeadline, oldEvery
	})
	if _, err := d.StartServices(handle, 9); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var body []byte
	for time.Now().Before(deadline) {
		body, err = os.ReadFile(out)
		if err == nil && strings.Contains(string(body), "3000 9 web") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(string(body), "3000 9 web") {
		t.Fatalf("injected env %q", body)
	}
	if strings.Contains(string(body), "http://") {
		t.Fatalf("injected origin %q", body)
	}
}

func TestHealthHTTPAndTCPAndDeadline(t *testing.T) {
	ln, port := listenLoopback(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/go", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/healthz")
		w.WriteHeader(http.StatusFound)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	d := Process{Root: t.TempDir()}
	handle, err := d.Create("box")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(handle, ".rusui"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(handle, ServicesRusuiPath), []byte("services:\n  web:\n    command: sleep 30\n    port: "+strconv.Itoa(port)+"\n    health: /healthz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := d.StartServices(handle, 1)
	if err != nil || len(got.Status) != 1 || got.Status[0].State != ServiceHealthy {
		t.Fatalf("http health %+v %v", got, err)
	}
	_ = d.StopServices(handle)

	if err := os.WriteFile(filepath.Join(handle, ServicesRusuiPath), []byte("services:\n  web:\n    command: sleep 30\n    port: "+strconv.Itoa(port)+"\n    health: /go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = d.StartServices(handle, 1)
	if err != nil || len(got.Status) != 1 || got.Status[0].State != ServiceHealthy {
		t.Fatalf("3xx health %+v %v", got, err)
	}
	_ = d.StopServices(handle)

	tcpLn, tcpPort := listenLoopback(t)
	t.Cleanup(func() { _ = tcpLn.Close() })
	if err := os.WriteFile(filepath.Join(handle, ServicesRusuiPath), []byte("services:\n  web:\n    command: sleep 30\n    port: "+strconv.Itoa(tcpPort)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = d.StartServices(handle, 1)
	if err != nil || len(got.Status) != 1 || got.Status[0].State != ServiceHealthy {
		t.Fatalf("tcp health %+v %v", got, err)
	}
	_ = d.StopServices(handle)

	oldDeadline, oldEvery := healthDeadline, healthProbeEvery
	healthDeadline = 80 * time.Millisecond
	healthProbeEvery = 10 * time.Millisecond
	t.Cleanup(func() {
		healthDeadline, healthProbeEvery = oldDeadline, oldEvery
	})
	missLn, miss := listenLoopback(t)
	_ = missLn.Close()
	if err := os.WriteFile(filepath.Join(handle, ServicesRusuiPath), []byte("services:\n  web:\n    command: sleep 30\n    port: "+strconv.Itoa(miss)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = d.StartServices(handle, 1)
	if err != nil || len(got.Status) != 1 || got.Status[0].State != ServiceUnhealthy {
		t.Fatalf("deadline %+v %v", got, err)
	}
	pidb, err := os.ReadFile(filepath.Join(handle, servicePIDDir, "web.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(pidb)))
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("unhealthy start still runs: %v", err)
	}
	_ = d.StopServices(handle)
}

func listenLoopback(t *testing.T) (net.Listener, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port
	if port < 1024 || port > 65535 {
		t.Fatalf("port %d", port)
	}
	return ln, port
}

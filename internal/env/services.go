package env

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	ServicesRusuiPath = ".rusui/services.yaml"
	servicePIDDir     = ".rusui/svc"

	ServiceStarting  = "starting"
	ServiceHealthy   = "healthy"
	ServiceUnhealthy = "unhealthy"
	ServiceExited    = "exited"
)

// healthDeadline is how long after launch the plane probes a declared
// port before recording unhealthy. A miss does not fail the start.
var healthDeadline = 15 * time.Second

var healthProbeEvery = 50 * time.Millisecond

var healthHTTP = &http.Client{
	Timeout: 300 * time.Millisecond,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

var serviceName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ParseError is an invalid services.yaml. Nothing starts; the session
// shows the error.
type ParseError struct {
	err error
}

func (e *ParseError) Error() string { return e.err.Error() }
func (e *ParseError) Unwrap() error { return e.err }

func parseErrorf(format string, args ...any) error {
	return &ParseError{err: fmt.Errorf("env: services.yaml: "+format, args...)}
}

func parseWrap(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*ParseError](err); ok {
		return err
	}
	return &ParseError{err: fmt.Errorf("env: services.yaml: %w", err)}
}

// AsParseError returns a services.yaml parse error, if err is one.
func AsParseError(err error) *ParseError {
	p, _ := errors.AsType[*ParseError](err)
	return p
}

// ServiceCtl starts and stops declared services.yaml processes. Start
// returns one capture per service it ran, when the driver keeps output.
type ServiceCtl interface {
	StartServices(handle string, sessionID int64) (StartedServices, error)
	StopServices(handle string) error
}

// StartedServices is the supervisor result of one start. Status is empty
// when the file is missing or invalid.
type StartedServices struct {
	Captures []Capture
	Status   []ServiceStatus
}

// ServiceStatus is one declared service after start and health probe.
type ServiceStatus struct {
	Name  string
	Port  int
	State string
}

type Service struct {
	Name    string
	Command string
	Cwd     string
	Env     map[string]string
	Port    int
	Health  string
}

type servicesFile struct {
	Services map[string]serviceSpec `yaml:"services"`
}

type serviceSpec struct {
	Command string            `yaml:"command"`
	Cwd     string            `yaml:"cwd"`
	Env     map[string]string `yaml:"env"`
	Port    *int              `yaml:"port"`
	Health  string            `yaml:"health"`
}

func ParseServices(data []byte) ([]Service, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var doc servicesFile
	if err := dec.Decode(&doc); err != nil {
		return nil, parseWrap(err)
	}
	if len(doc.Services) == 0 {
		return nil, parseErrorf("services mapping is required")
	}
	names := make([]string, 0, len(doc.Services))
	for name := range doc.Services {
		names = append(names, name)
	}
	slices.Sort(names)
	ports := map[int]string{}
	out := make([]Service, 0, len(names))
	for _, name := range names {
		if !serviceName.MatchString(name) {
			return nil, parseErrorf("invalid service name %q", name)
		}
		spec := doc.Services[name]
		if strings.TrimSpace(spec.Command) == "" {
			return nil, parseErrorf("service %q missing command", name)
		}
		svc := Service{Name: name, Command: spec.Command, Cwd: spec.Cwd, Env: spec.Env, Health: spec.Health}
		if spec.Port != nil {
			if *spec.Port < 1024 || *spec.Port > 65535 {
				return nil, parseErrorf("service %q port %d out of range", name, *spec.Port)
			}
			if other, ok := ports[*spec.Port]; ok {
				return nil, parseErrorf("services %q and %q share port %d", other, name, *spec.Port)
			}
			ports[*spec.Port] = name
			svc.Port = *spec.Port
		}
		if spec.Health != "" {
			if svc.Port == 0 {
				return nil, parseErrorf("service %q health requires port", name)
			}
			if err := validHealthPath(spec.Health); err != nil {
				return nil, parseErrorf("service %q %v", name, err)
			}
		}
		out = append(out, svc)
	}
	return out, nil
}

func validHealthPath(health string) error {
	if !strings.HasPrefix(health, "/") {
		return fmt.Errorf("health %q must be an absolute HTTP path", health)
	}
	u, err := url.Parse(health)
	if err != nil || u.Scheme != "" || u.Host != "" || strings.Contains(health, "://") {
		return fmt.Errorf("health %q is a path, not a URL", health)
	}
	return nil
}

func loadServices(read func(path string) ([]byte, error)) ([]Service, error) {
	data, err := read(ServicesRusuiPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return ParseServices(data)
}

// WorkspaceServices reads `.rusui/services.yaml` from an environment.
// A missing file is an empty list.
func WorkspaceServices(d Driver, handle string) ([]Service, error) {
	return loadServices(workspaceReader(d, handle))
}

func workspaceReader(d Driver, handle string) func(string) ([]byte, error) {
	switch x := d.(type) {
	case Process:
		return func(path string) ([]byte, error) { return readWorkspaceFile(handle, path) }
	case *Process:
		return func(path string) ([]byte, error) { return readWorkspaceFile(handle, path) }
	case Container:
		return func(path string) ([]byte, error) { return readRuntimeFile(x.RT, handle, path) }
	case *Container:
		return func(path string) ([]byte, error) { return readRuntimeFile(x.RT, handle, path) }
	default:
		return func(string) ([]byte, error) { return nil, os.ErrNotExist }
	}
}

func readWorkspaceFile(root, path string) ([]byte, error) {
	return os.ReadFile(filepath.Join(root, path))
}

// ServiceNamed returns the declared service with name.
func ServiceNamed(svcs []Service, name string) (Service, bool) {
	for _, svc := range svcs {
		if svc.Name == name {
			return svc, true
		}
	}
	return Service{}, false
}

// StartServices starts detached processes. The process driver keeps no
// service output.
func (p Process) StartServices(handle string, sessionID int64) (StartedServices, error) {
	svcs, err := loadServices(func(path string) ([]byte, error) {
		return readWorkspaceFile(handle, path)
	})
	if err != nil || len(svcs) == 0 {
		return StartedServices{}, err
	}
	if err := os.MkdirAll(filepath.Join(handle, servicePIDDir), 0o700); err != nil {
		return StartedServices{}, err
	}
	for _, svc := range svcs {
		if err := startProcessService(handle, sessionID, svc); err != nil {
			_ = p.StopServices(handle)
			return StartedServices{}, err
		}
	}
	status := make([]ServiceStatus, 0, len(svcs))
	for _, svc := range svcs {
		st := ServiceStatus{Name: svc.Name, Port: svc.Port, State: ServiceStarting}
		if !processServiceAlive(handle, svc.Name) {
			st.State = ServiceExited
		} else if svc.Port == 0 {
			st.State = ServiceHealthy
		} else {
			st.State = probeHealth(p, handle, svc, false)
			if st.State == ServiceUnhealthy && !processServiceAlive(handle, svc.Name) {
				st.State = ServiceExited
			}
		}
		status = append(status, st)
	}
	return StartedServices{Status: status}, nil
}

func (p Process) StopServices(handle string) error {
	dir := filepath.Join(handle, servicePIDDir)
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var first error
	for _, ent := range ents {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".pid") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || pid <= 0 {
			continue
		}
		if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !os.IsNotExist(err) {
			if first == nil {
				first = err
			}
		}
		_ = os.Remove(filepath.Join(dir, ent.Name()))
	}
	return first
}

func startProcessService(handle string, sessionID int64, svc Service) error {
	cmd := exec.Command("sh", "-c", svc.Command)
	cmd.Dir = handle
	if svc.Cwd != "" {
		cmd.Dir = filepath.Join(handle, svc.Cwd)
	}
	cmd.Env = serviceEnv(sessionID, svc)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	return os.WriteFile(filepath.Join(handle, servicePIDDir, svc.Name+".pid"), []byte(strconv.Itoa(pid)+"\n"), 0o600)
}

func processServiceAlive(handle, name string) bool {
	b, err := os.ReadFile(filepath.Join(handle, servicePIDDir, name+".pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func serviceEnv(sessionID int64, svc Service) []string {
	env := []string{"PATH=" + os.Getenv("PATH")}
	if home := os.Getenv("HOME"); home != "" {
		env = append(env, "HOME="+home)
	}
	keys := make([]string, 0, len(svc.Env))
	for k := range svc.Env {
		if k == "PORT" || k == "RUSUI_SESSION_ID" || k == "RUSUI_SERVICE" {
			continue
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		env = append(env, k+"="+svc.Env[k])
	}
	if svc.Port != 0 {
		env = append(env, "PORT="+strconv.Itoa(svc.Port))
	}
	env = append(env, "RUSUI_SESSION_ID="+strconv.FormatInt(sessionID, 10), "RUSUI_SERVICE="+svc.Name)
	return env
}

func serviceExport(sessionID int64, svc Service) string {
	var b strings.Builder
	keys := make([]string, 0, len(svc.Env))
	for k := range svc.Env {
		if k == "PORT" || k == "RUSUI_SESSION_ID" || k == "RUSUI_SERVICE" {
			continue
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(strconv.Quote(svc.Env[k]))
		b.WriteByte(' ')
	}
	if svc.Port != 0 {
		b.WriteString("PORT=")
		b.WriteString(strconv.Quote(strconv.Itoa(svc.Port)))
		b.WriteByte(' ')
	}
	b.WriteString("RUSUI_SESSION_ID=")
	b.WriteString(strconv.Quote(strconv.FormatInt(sessionID, 10)))
	b.WriteString(" RUSUI_SERVICE=")
	b.WriteString(strconv.Quote(svc.Name))
	b.WriteByte(' ')
	return b.String()
}

func (c Container) StartServices(handle string, sessionID int64) (StartedServices, error) {
	svcs, err := loadServices(func(path string) ([]byte, error) {
		return readRuntimeFile(c.RT, handle, path)
	})
	if err != nil || len(svcs) == 0 {
		return StartedServices{}, err
	}
	var caps []Capture
	for _, svc := range svcs {
		cmd := containerServiceCmd(sessionID, svc)
		captured, err := c.capture(handle, CaptureService, svc.Name, cmd)
		caps = append(caps, captured)
		if err != nil {
			return StartedServices{Captures: caps}, err
		}
	}
	status := make([]ServiceStatus, 0, len(svcs))
	for i, svc := range svcs {
		st := ServiceStatus{Name: svc.Name, Port: svc.Port, State: ServiceStarting}
		if caps[i].Failed {
			st.State = ServiceExited
		} else if svc.Port == 0 {
			st.State = ServiceHealthy
		} else {
			st.State = probeHealth(c, handle, svc, false)
		}
		status = append(status, st)
	}
	return StartedServices{Captures: caps, Status: status}, nil
}

func containerServiceCmd(sessionID int64, svc Service) []string {
	prefix := serviceExport(sessionID, svc)
	if svc.Cwd != "" {
		return []string{"/bin/sh", "-c", prefix + "cd \"$1\" && shift && eval \"$1\"", "rusui-svc", svc.Cwd, svc.Command}
	}
	return []string{"/bin/sh", "-c", prefix + "eval \"$1\"", "rusui-svc", svc.Command}
}

func (c Container) StopServices(string) error {
	return nil
}

func readRuntimeFile(rt Runtime, id, path string) ([]byte, error) {
	if rt == nil || !rt.HasFile(id, path) {
		return nil, os.ErrNotExist
	}
	return rt.ReadFile(id, path)
}

// ProbeService checks health once. Ensure does not restart.
func ProbeService(d Driver, handle string, svc Service) string {
	if svc.Port == 0 {
		return ServiceUnhealthy
	}
	return probeHealth(d, handle, svc, true)
}

func probeHealth(d Driver, handle string, svc Service, once bool) string {
	if svc.Port == 0 {
		return ServiceHealthy
	}
	deadline := time.Now().Add(healthDeadline)
	for {
		if healthOK(d, handle, svc) {
			return ServiceHealthy
		}
		if once || !time.Now().Before(deadline) {
			return ServiceUnhealthy
		}
		time.Sleep(healthProbeEvery)
	}
}

func healthOK(d Driver, handle string, svc Service) bool {
	pf, ok := d.(PortForwarder)
	if !ok {
		return false
	}
	dial, err := pf.PortForward(handle, svc.Port)
	if err != nil {
		return false
	}
	if svc.Health == "" {
		c, err := net.DialTimeout("tcp", dial, 200*time.Millisecond)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	}
	u := "http://" + dial + svc.Health
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return false
	}
	res, err := healthHTTP.Do(req)
	if err != nil {
		return false
	}
	_ = res.Body.Close()
	return res.StatusCode >= 200 && res.StatusCode < 400
}

package setup

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type serviceCommand struct {
	name string
	args []string
}

type fakeServiceCommands struct {
	active   bool
	linger   bool
	source   string
	unitPath string
	failCall string
	calls    []serviceCommand
}

func (f *fakeServiceCommands) Run(name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, serviceCommand{name: name, args: append([]string(nil), args...)})
	if len(args) > 0 && args[0] == f.failCall || len(args) > 1 && args[1] == f.failCall {
		return nil, errors.New("simulated command failure")
	}
	if name == "loginctl" && len(args) > 0 {
		switch args[0] {
		case "show-user":
			if f.linger {
				return []byte("yes\n"), nil
			}
			return []byte("no\n"), nil
		case "enable-linger":
			f.linger = true
		}
	}
	if name == "launchctl" && len(args) > 1 && args[0] == "print" {
		if !f.active {
			return nil, &exec.ExitError{}
		}
		return []byte("path = " + f.source + "\n"), nil
	}
	if name == "systemctl" && len(args) > 3 && args[1] == "show" {
		if f.source == "" {
			return []byte("(null)\n"), nil
		}
		return []byte(f.source + "\n"), nil
	}
	if name == "systemctl" && len(args) > 2 && args[1] == "is-active" {
		if !f.active {
			return nil, &exec.ExitError{}
		}
		return nil, nil
	}
	if name == "launchctl" && len(args) > 0 {
		switch args[0] {
		case "bootstrap":
			f.active = true
			f.source = args[len(args)-1]
		case "bootout":
			f.active = false
			f.source = ""
		}
	}
	if name == "systemctl" && len(args) > 2 {
		switch args[1] {
		case "enable":
			f.active = true
			f.source = f.unitPath
		case "restart":
			f.active = true
		case "stop", "disable":
			f.active = false
		case "daemon-reload":
		}
	}
	return nil, nil
}

func (f *fakeServiceCommands) mutationCount() int {
	mutations := 0
	for _, call := range f.calls {
		if call.name == "launchctl" && len(call.args) > 0 && (call.args[0] == "bootstrap" || call.args[0] == "bootout") {
			mutations++
		}
		if call.name == "systemctl" && len(call.args) > 1 {
			switch call.args[1] {
			case "enable", "restart", "stop", "disable":
				mutations++
			}
		}
		if call.name == "loginctl" && len(call.args) > 0 && call.args[0] == "enable-linger" {
			mutations++
		}
	}
	return mutations
}

func serviceOptions(home, state string, runner serviceCommandRunner) (Options, Paths) {
	values := map[string]string{"HOME": home, "PATH": "/usr/bin:/bin"}
	getenv := func(key string) string { return values[key] }
	p := PathsFor(state)
	return Options{
		StateDir: state,
		Addr:     "127.0.0.1:18080",
		Getenv:   getenv,
		ServiceManager: &nativeServiceManager{
			platform: "launchd",
			runner:   runner,
		},
	}, p
}

func TestLaunchdUserServicePlanApplyAndRemove(t *testing.T) {
	home, state := t.TempDir(), t.TempDir()
	p := PathsFor(state)
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.TLS, 0o700); err != nil {
		t.Fatal(err)
	}
	const token = "test-token-never-print"
	if err := os.WriteFile(p.Env, []byte("RUSUI_GITHUB_TOKEN='"+token+"'\nRUSUI_WORKER_SECRET=test-worker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.PlaneCert, []byte("test plane certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeServiceCommands{}
	o, _ := serviceOptions(home, state, runner)
	manager := o.ServiceManager.(*nativeServiceManager)

	plan, err := manager.Plan(o, p)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Action != Create || plan.Item != "user service" || strings.Contains(plan.Detail, token) {
		t.Fatalf("service plan leaked or misreported data: %+v", plan)
	}
	if _, err := manager.Apply(o, p); err != nil {
		t.Fatal(err)
	}
	path, err := manager.servicePath(o)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(content, []byte("<string>-addr</string>")) || !bytes.Contains(content, []byte("<string>127.0.0.1:18080</string>")) || !bytes.Contains(content, []byte(token)) || bytes.Contains(content, []byte("'"+token+"'")) || !bytes.Contains(content, []byte("rusui.log")) {
		t.Fatalf("service plist missing arguments or environment: %s", content)
	}
	if !bytes.Contains(content, []byte("rusui-inputs-sha256=")) {
		t.Fatalf("service plist lacks its configuration change marker: %s", content)
	}
	decoder := xml.NewDecoder(bytes.NewReader(content))
	for {
		if _, err := decoder.Token(); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("invalid plist XML: %v", err)
		}
	}
	if mode(t, path) != 0o600 {
		t.Fatalf("plist permissions=%#o, want 0600", mode(t, path))
	}
	mutations := runner.mutationCount()
	if _, err := manager.Apply(o, p); err != nil {
		t.Fatal(err)
	}
	if runner.mutationCount() != mutations {
		t.Fatalf("reapply restarted active service: before=%v after=%v", mutations, runner.mutationCount())
	}
	updated := o
	updated.Addr = "127.0.0.1:18082"
	runner.failCall = "bootout"
	if _, err := manager.Apply(updated, p); err == nil {
		t.Fatal("update succeeded despite stop failure")
	}
	contentAfterStopFailure, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(content, contentAfterStopFailure) {
		t.Fatalf("failed stop replaced the active service file: err=%v", err)
	}
	runner.failCall = ""
	if _, err := manager.Apply(updated, p); err != nil {
		t.Fatal(err)
	}
	content, err = os.ReadFile(path)
	if err != nil || !bytes.Contains(content, []byte("127.0.0.1:18082")) {
		t.Fatalf("service update did not install new address: err=%v", err)
	}
	beforeTLSUpdate := runner.mutationCount()
	if err := os.WriteFile(p.PlaneCert, []byte("rotated plane certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Apply(updated, p); err != nil {
		t.Fatal(err)
	}
	if runner.mutationCount() <= beforeTLSUpdate {
		t.Fatal("launchd service was not restarted after TLS material changed")
	}
	if err := os.WriteFile(p.DB, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err := manager.Remove(o)
	if err != nil {
		t.Fatal(err)
	}
	if removed.Action != Remove {
		t.Fatalf("remove step %+v", removed)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("service file remains: %v", err)
	}
	if data, err := os.ReadFile(p.DB); err != nil || string(data) != "preserve" {
		t.Fatalf("remove-service changed state database: %q, %v", data, err)
	}
}

func TestSystemdServiceApplyReapplyAndRemove(t *testing.T) {
	home, config, state := t.TempDir(), t.TempDir(), t.TempDir()
	values := map[string]string{"HOME": home, "XDG_CONFIG_HOME": config, "PATH": "/usr/bin:/bin"}
	runner := &fakeServiceCommands{}
	manager := &nativeServiceManager{platform: "systemd", runner: runner}
	o := Options{
		StateDir:       state,
		Addr:           "127.0.0.1:18081",
		Getenv:         func(key string) string { return values[key] },
		ServiceManager: manager,
	}
	p := PathsFor(state)
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.TLS, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Env, []byte("export RUSUI_GITHUB_TOKEN='unit-test'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := manager.servicePath(o)
	if err != nil {
		t.Fatal(err)
	}
	runner.unitPath = path
	plan, err := manager.Plan(o, p)
	if err != nil || !strings.Contains(plan.Detail, "setup will enable user lingering") {
		t.Fatalf("systemd plan=%+v err=%v, want boot/logout persistence", plan, err)
	}
	runner.failCall = "enable-linger"
	if _, err := manager.Apply(o, p); err == nil {
		t.Fatal("systemd apply succeeded despite lingering setup failure")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("systemd unit was installed without boot/logout persistence: %v", err)
	}
	runner.failCall = ""
	if _, err := manager.Apply(o, p); err != nil {
		t.Fatal(err)
	}
	if !runner.linger {
		t.Fatal("systemd service did not enable user lingering")
	}
	unit, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	envPath := systemdEnvironmentPath(path)
	serviceEnv, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(unit, []byte("EnvironmentFile="+systemdPathValue(envPath))) || bytes.Contains(unit, []byte("RUSUI_GITHUB_TOKEN=unit-test")) {
		t.Fatalf("systemd unit must load the normalized environment file without copying secrets:\n%s", unit)
	}
	if !bytes.Contains(serviceEnv, []byte("RUSUI_GITHUB_TOKEN=\"unit-test\"")) || bytes.Contains(serviceEnv, []byte("export ")) {
		t.Fatalf("systemd environment file did not normalize the configured token:\n%s", serviceEnv)
	}
	if !bytes.Contains(unit, []byte("rusui-inputs-sha256=")) {
		t.Fatalf("systemd unit lacks its configuration change marker:\n%s", unit)
	}
	if mode(t, path) != 0o600 {
		t.Fatalf("unit permissions=%#o, want 0600", mode(t, path))
	}
	if mode(t, envPath) != 0o600 {
		t.Fatalf("systemd environment permissions=%#o, want 0600", mode(t, envPath))
	}
	rotating := o
	rotating.RotateTLS = true
	rotationPlan, err := manager.Plan(rotating, p)
	if err != nil || rotationPlan.Action != Update {
		t.Fatalf("TLS rotation plan=%+v err=%v, want service update", rotationPlan, err)
	}
	mutations := runner.mutationCount()
	if _, err := manager.Apply(o, p); err != nil {
		t.Fatal(err)
	}
	if runner.mutationCount() != mutations {
		t.Fatalf("reapply restarted active service: before=%v after=%v", mutations, runner.mutationCount())
	}
	beforeEnvUpdate := runner.mutationCount()
	if err := os.WriteFile(p.Env, []byte("export RUSUI_GITHUB_TOKEN=\"updated-unit-test\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Apply(o, p); err != nil {
		t.Fatal(err)
	}
	if runner.mutationCount() <= beforeEnvUpdate {
		t.Fatal("systemd service was not restarted after environment changed")
	}
	unit, err = os.ReadFile(path)
	if err != nil || bytes.Contains(unit, []byte("updated-unit-test")) {
		t.Fatalf("systemd unit should not copy environment secrets: err=%v", err)
	}
	serviceEnv, err = os.ReadFile(envPath)
	if err != nil || !bytes.Contains(serviceEnv, []byte("RUSUI_GITHUB_TOKEN=\"updated-unit-test\"")) {
		t.Fatalf("systemd environment file did not update from export syntax: err=%v", err)
	}
	beforeTLSUpdate := runner.mutationCount()
	if err := os.WriteFile(p.PlaneCert, []byte("rotated plane certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Apply(o, p); err != nil {
		t.Fatal(err)
	}
	if runner.mutationCount() <= beforeTLSUpdate {
		t.Fatal("systemd service was not restarted after TLS material changed")
	}
	updated := o
	updated.Addr = "127.0.0.1:18083"
	runner.failCall = "daemon-reload"
	if _, err := manager.Apply(updated, p); err == nil {
		t.Fatal("systemd update succeeded despite daemon-reload failure")
	}
	runner.failCall = ""
	if _, err := manager.Apply(updated, p); err != nil {
		t.Fatalf("retry after daemon-reload failure: %v", err)
	}
	unit, err = os.ReadFile(path)
	if err != nil || !bytes.Contains(unit, []byte("127.0.0.1:18083")) || !runner.active {
		t.Fatalf("systemd retry did not activate updated unit: active=%v err=%v", runner.active, err)
	}
	if err := os.WriteFile(p.DB, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if step, err := manager.Remove(o); err != nil || step.Action != Remove {
		t.Fatalf("remove=%+v err=%v", step, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("systemd unit remains: %v", err)
	}
	if _, err := os.Stat(envPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("systemd environment file remains: %v", err)
	}
	if values := ReadEnv(p.Env); values["RUSUI_GITHUB_TOKEN"] != "updated-unit-test" {
		t.Fatalf("remove-service changed the operator environment file: %#v", values)
	}
	if !runner.linger {
		t.Fatal("remove-service disabled account-wide user lingering")
	}
	if data, err := os.ReadFile(p.DB); err != nil || string(data) != "preserve" {
		t.Fatalf("remove-service changed state database: %q, %v", data, err)
	}
}

func TestSystemdUnitUsesEnvironmentFileAndLoopback(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state with spaces%\\literal\"quote")
	p := PathsFor(state)
	envPath := filepath.Join(t.TempDir(), "systemd user%\\literal\"quote", "rusui.service.env")
	pathEnv := "/opt/tools with spaces/$literal%spec:/bin"
	content, err := systemdUnit(p, "/usr/local/bin/rusui", "127.0.0.1:8080", pathEnv, "test-input-digest", envPath)
	if err != nil {
		t.Fatal(err)
	}
	unit := string(content)
	for _, want := range []string{
		systemdUnitMarker,
		"WorkingDirectory=" + systemdPathValue(p.Dir),
		"EnvironmentFile=" + systemdPathValue(envPath),
		"Environment=" + systemdEnvironmentValue("PATH="+pathEnv),
		"\"-addr\" \"127.0.0.1:8080\"",
		"\"-db\" " + systemdQuote(p.DB),
		"Restart=on-failure",
		"WantedBy=default.target",
		"rusui-inputs-sha256=test-input-digest",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("systemd unit missing %q:\n%s", want, unit)
		}
	}
	if strings.Contains(unit, "RUSUI_GITHUB_TOKEN=") {
		t.Fatalf("systemd unit copied a secret instead of loading the env file:\n%s", unit)
	}
	if err := validateServiceAddr("0.0.0.0:8080"); err == nil {
		t.Fatal("non-loopback service address accepted")
	}
	if err := validateServiceAddr("127.0.0.1:0"); err == nil {
		t.Fatal("ephemeral service port accepted")
	}
	if err := validateServiceAddr("127.0.0.2:8080"); err == nil {
		t.Fatal("loopback address outside generated TLS SANs accepted")
	}
	if err := validateServiceAddr("[::1]:8080"); err == nil {
		t.Fatal("IPv6 address outside generated TLS SANs accepted")
	}
	if err := validateServiceAddr("localhost:8080"); err != nil {
		t.Fatalf("certificate hostname localhost rejected: %v", err)
	}
}

func TestSystemdEnvironmentFileNormalizesExportsAndQuotes(t *testing.T) {
	source := filepath.Join(t.TempDir(), "rusui.env")
	contents := "export RUSUI_GITHUB_TOKEN='token with spaces'\nRUSUI_MODEL_UPSTREAM=\"https://proxy.example/path?a=b\"\nRUSUI_WORKER_SECRET=plain\n"
	if err := os.WriteFile(source, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := systemdEnvironmentFile(source)
	if err != nil {
		t.Fatal(err)
	}
	want := systemdEnvMarker + "\nRUSUI_GITHUB_TOKEN=\"token with spaces\"\nRUSUI_MODEL_UPSTREAM=\"https://proxy.example/path?a=b\"\nRUSUI_WORKER_SECRET=\"plain\"\n"
	if string(got) != want {
		t.Fatalf("normalized systemd environment = %q, want %q", got, want)
	}
}

func TestSystemdServiceRefusesUnmanagedEnvironmentFile(t *testing.T) {
	home, config, state := t.TempDir(), t.TempDir(), t.TempDir()
	runner := &fakeServiceCommands{}
	manager := &nativeServiceManager{platform: "systemd", runner: runner}
	o := Options{
		StateDir: state,
		Addr:     "127.0.0.1:18080",
		Getenv: func(key string) string {
			if key == "HOME" {
				return home
			}
			if key == "XDG_CONFIG_HOME" {
				return config
			}
			return "/usr/bin:/bin"
		},
		ServiceManager: manager,
	}
	unitPath, err := manager.servicePath(o)
	if err != nil {
		t.Fatal(err)
	}
	envPath := systemdEnvironmentPath(unitPath)
	if err := os.MkdirAll(filepath.Dir(envPath), 0o700); err != nil {
		t.Fatal(err)
	}
	const owned = "operator-owned environment file\n"
	if err := os.WriteFile(envPath, []byte(owned), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := manager.Plan(o, PathsFor(state))
	if err != nil || plan.Action != NeedsYou {
		t.Fatalf("plan=%+v err=%v, want conflict", plan, err)
	}
	if _, err := manager.Apply(o, PathsFor(state)); err == nil {
		t.Fatal("apply overwrote unmanaged systemd environment file")
	}
	if contents, err := os.ReadFile(envPath); err != nil || string(contents) != owned {
		t.Fatalf("unmanaged environment file changed: %q, %v", contents, err)
	}
}

func TestServiceRefusesUnmanagedFile(t *testing.T) {
	home, state := t.TempDir(), t.TempDir()
	runner := &fakeServiceCommands{}
	o, p := serviceOptions(home, state, runner)
	manager := o.ServiceManager.(*nativeServiceManager)
	path, err := manager.servicePath(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("operator-owned file"), 0o600); err != nil {
		t.Fatal(err)
	}
	step, err := manager.Plan(o, p)
	if err != nil || step.Action != NeedsYou {
		t.Fatalf("plan=%+v err=%v, want conflict", step, err)
	}
	if _, err := manager.Apply(o, p); err == nil {
		t.Fatal("apply overwrote unmanaged service file")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "operator-owned file" {
		t.Fatalf("unmanaged file changed: %q, %v", data, err)
	}
}

type testServiceManager struct {
	plans     int
	applies   int
	removes   int
	planPaths Paths
}

func (m *testServiceManager) Plan(_ Options, paths Paths) (Step, error) {
	m.plans++
	m.planPaths = paths
	return Step{Action: Create, Item: "user service", Detail: "127.0.0.1"}, nil
}

func (m *testServiceManager) Apply(Options, Paths) (Step, error) {
	m.applies++
	return Step{Action: Create, Item: "user service", Detail: "127.0.0.1"}, nil
}

func (m *testServiceManager) Remove(Options) (Step, error) {
	m.removes++
	return Step{Action: Remove, Item: "user service"}, nil
}

func TestSetupPlanApplyAndRemoveIncludeUserService(t *testing.T) {
	manager := &testServiceManager{}
	o := Options{StateDir: filepath.Join(t.TempDir(), "state"), ServiceManager: manager}
	plan, err := Plan(o)
	if err != nil {
		t.Fatal(err)
	}
	if actions(plan)["user service"] != Create || manager.plans != 1 {
		t.Fatalf("plan=%v calls=%d", actions(plan), manager.plans)
	}
	if _, err := Apply(o); err != nil {
		t.Fatal(err)
	}
	if manager.applies != 1 {
		t.Fatalf("apply calls=%d", manager.applies)
	}
	step, err := RemoveService(o)
	if err != nil || step.Action != Remove || manager.removes != 1 {
		t.Fatalf("remove=%+v calls=%d err=%v", step, manager.removes, err)
	}
}

func TestServiceSetupRequiresPersistentExternalSecrets(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	getenv := func(key string) string {
		return "exported-value"
	}
	serviceOptions := Options{StateDir: state, Getenv: getenv, ServiceManager: &testServiceManager{}}
	steps, err := Plan(serviceOptions)
	if err != nil {
		t.Fatal(err)
	}
	needsPersistentToken := false
	for _, step := range steps {
		if step.Action == NeedsYou && step.Item == "RUSUI_GITHUB_TOKEN" && strings.Contains(step.Detail, PathsFor(state).Env) {
			needsPersistentToken = true
		}
	}
	if !needsPersistentToken {
		t.Fatalf("service plan should require the persistent env file token: %+v", steps)
	}

	oneShotSteps, err := Plan(Options{StateDir: filepath.Join(t.TempDir(), "state"), Getenv: getenv})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range oneShotSteps {
		if step.Action == NeedsYou && step.Item == "RUSUI_GITHUB_TOKEN" {
			t.Fatalf("one-shot setup did not use the exported token: %+v", oneShotSteps)
		}
	}
}

func TestSetupPlanResolvesRelativeStatePath(t *testing.T) {
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	manager := &testServiceManager{}
	if _, err := Plan(Options{StateDir: "state", ServiceManager: manager}); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(workingDir, "state")
	if manager.planPaths.Dir != want || !filepath.IsAbs(manager.planPaths.Policy) {
		t.Fatalf("service paths are not absolute: %+v", manager.planPaths)
	}
}

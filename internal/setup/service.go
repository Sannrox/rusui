package setup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const defaultServiceAddr = "127.0.0.1:8080"

const (
	launchdLabel      = "com.sannrox.rusui"
	systemdUnitName   = "rusui.service"
	launchdMarker     = "<!-- managed by rusui setup -->"
	systemdUnitMarker = "# Managed by rusui setup"
	systemdEnvMarker  = "# Managed by rusui setup environment"
)

// ServiceManager owns one per-user service definition and its lifecycle.
type ServiceManager interface {
	Plan(Options, Paths) (Step, error)
	Apply(Options, Paths) (Step, error)
	Remove(Options) (Step, error)
}

type serviceCommandRunner interface {
	Run(string, ...string) ([]byte, error)
}

type execServiceCommandRunner struct{}

func (execServiceCommandRunner) Run(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	return cmd.CombinedOutput()
}

type nativeServiceManager struct {
	platform string
	runner   serviceCommandRunner
}

// PlatformServiceManager selects launchd or systemd --user on supported hosts.
func PlatformServiceManager() ServiceManager {
	switch runtime.GOOS {
	case "darwin":
		return &nativeServiceManager{platform: "launchd", runner: execServiceCommandRunner{}}
	case "linux":
		return &nativeServiceManager{platform: "systemd", runner: execServiceCommandRunner{}}
	default:
		return unsupportedServiceManager{platform: runtime.GOOS}
	}
}

// RemoveService uninstalls the user service while preserving its state files.
func RemoveService(o Options) (Step, error) {
	if o.ServiceManager == nil {
		return Step{Action: Skip, Item: "user service", Detail: "no service manager available"}, nil
	}
	return o.ServiceManager.Remove(o)
}

func (m *nativeServiceManager) Plan(o Options, p Paths) (Step, error) {
	needsEnvNormalization := false
	if m.platform == "systemd" {
		raw, err := os.ReadFile(p.Env)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Step{}, fmt.Errorf("setup: read environment file: %w", err)
		}
		normalized, err := systemdEnvFileContent(raw)
		if err != nil {
			return Step{}, err
		}
		needsEnvNormalization = err == nil && raw != nil && !bytes.Equal(raw, normalized)
	}
	path, content, addr, err := m.desired(o, p)
	if err != nil {
		return Step{}, err
	}
	old, readErr := os.ReadFile(path)
	action := Create
	switch {
	case readErr == nil && !m.managed(old):
		return Step{Action: NeedsYou, Item: "user service", Detail: "unmanaged service file exists at " + path}, nil
	case readErr == nil && bytes.Equal(old, content):
		if o.RotateTLS {
			action = Update
		} else {
			action = Keep
		}
	case readErr != nil && !errors.Is(readErr, os.ErrNotExist):
		return Step{}, fmt.Errorf("setup: read service file: %w", readErr)
	case readErr == nil:
		action = Update
	}
	if action == Keep && needsEnvNormalization {
		action = Update
	}
	if m.platform == "systemd" {
		envPath := systemdEnvironmentPath(path)
		expected, err := systemdEnvironmentFile(p.Env)
		if err != nil {
			return Step{}, err
		}
		oldEnv, envErr := os.ReadFile(envPath)
		switch {
		case envErr == nil && !managedSystemdEnvironment(oldEnv):
			return Step{Action: NeedsYou, Item: "user service environment", Detail: "unmanaged service environment file exists at " + envPath}, nil
		case envErr == nil && bytes.Equal(oldEnv, expected):
		case errors.Is(envErr, os.ErrNotExist):
			if action == Keep {
				action = Update
			}
		case envErr != nil:
			return Step{}, fmt.Errorf("setup: read service environment file: %w", envErr)
		default:
			if action == Keep {
				action = Update
			}
		}
	}
	active, source, err := m.current(o, path)
	if err != nil {
		return Step{}, err
	}
	if (active || source != "") && !sameServicePath(source, path) {
		return Step{Action: NeedsYou, Item: "user service", Detail: "service name is active from another file"}, nil
	}
	detail := fmt.Sprintf("%s (%s)", path, addr)
	if m.platform == "systemd" {
		lingering, err := m.lingeringEnabled()
		if err != nil {
			return Step{}, err
		}
		if lingering {
			detail += "; user lingering is enabled"
		} else {
			detail += "; setup will enable user lingering for boot and logout persistence"
		}
	}
	return Step{Action: action, Item: "user service", Detail: detail}, nil
}

func (m *nativeServiceManager) Apply(o Options, p Paths) (Step, error) {
	step, err := m.Plan(o, p)
	if err != nil {
		return Step{}, err
	}
	if step.Action == NeedsYou {
		return Step{}, fmt.Errorf("setup: %s", step.Detail)
	}
	if m.platform == "systemd" {
		if err := m.ensureLingering(); err != nil {
			return Step{}, err
		}
	}
	path, content, _, err := m.desired(o, p)
	if err != nil {
		return Step{}, err
	}
	changed := step.Action == Create || step.Action == Update
	active, source, err := m.current(o, path)
	if err != nil {
		return Step{}, err
	}
	if (active || source != "") && !sameServicePath(source, path) {
		return Step{}, fmt.Errorf("setup: service name is active from another file")
	}
	if changed && active {
		if err := m.deactivate(o, path); err != nil {
			return Step{}, err
		}
		active = false
	}
	if m.platform == "systemd" {
		if err := writeSystemdEnvironmentFile(path, p.Env); err != nil {
			return Step{}, err
		}
	}
	if changed {
		if m.platform == "systemd" {
			if err := normalizeSystemdEnvFile(p.Env); err != nil {
				return Step{}, err
			}
		}
		if err := writeServiceFile(path, content); err != nil {
			return Step{}, err
		}
	}
	if err := m.activate(o, path, changed, active); err != nil {
		return Step{}, err
	}
	return step, nil
}

func (m *nativeServiceManager) Remove(o Options) (Step, error) {
	path, err := m.servicePath(o)
	if err != nil {
		return Step{}, err
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if m.platform == "systemd" {
			if err := removeSystemdEnvironmentFile(path); err != nil {
				return Step{}, err
			}
		}
		return Step{Action: Keep, Item: "user service", Detail: "not installed"}, nil
	}
	if err != nil {
		return Step{}, fmt.Errorf("setup: read service file: %w", err)
	}
	if !m.managed(content) {
		return Step{}, fmt.Errorf("setup: refusing to remove unmanaged service file at %s", path)
	}
	if m.platform == "systemd" {
		if err := checkSystemdEnvironmentFile(path); err != nil {
			return Step{}, err
		}
	}
	active, source, err := m.current(o, path)
	if err != nil {
		return Step{}, err
	}
	if (active || source != "") && !sameServicePath(source, path) {
		return Step{}, fmt.Errorf("setup: service name is active from another file")
	}
	if active {
		if err := m.deactivate(o, path); err != nil {
			return Step{}, err
		}
	}
	if m.platform == "systemd" {
		if _, err := m.runner.Run("systemctl", "--user", "disable", systemdUnitName); err != nil {
			return Step{}, commandError("disable systemd user service", err)
		}
	}
	if err := os.Remove(path); err != nil {
		return Step{}, fmt.Errorf("setup: remove service file: %w", err)
	}
	if m.platform == "systemd" {
		if err := removeSystemdEnvironmentFile(path); err != nil {
			return Step{}, err
		}
		if _, err := m.runner.Run("systemctl", "--user", "daemon-reload"); err != nil {
			return Step{}, commandError("reload systemd user units", err)
		}
	}
	return Step{Action: Remove, Item: "user service", Detail: path}, nil
}

func sameServicePath(source, path string) bool {
	if source == "" {
		return false
	}
	resolvedSource, sourceErr := filepath.EvalSymlinks(source)
	resolvedPath, pathErr := filepath.EvalSymlinks(path)
	if sourceErr == nil && pathErr == nil {
		return resolvedSource == resolvedPath
	}
	return filepath.Clean(source) == filepath.Clean(path)
}

func serviceInputsDigest(p Paths, binary string) (string, error) {
	h := sha256.New()
	for _, path := range []string{binary, p.Env, p.Policy, p.CACert, p.PlaneCert, p.PlaneKey} {
		_, _ = io.WriteString(h, filepath.Clean(path)+"\x00")
		if path == p.Env {
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				_, _ = io.WriteString(h, "missing\x00")
				continue
			} else if err != nil {
				return "", fmt.Errorf("setup: stat service input %s: %w", path, err)
			}
			values := readEnv(path)
			keys := make([]string, 0, len(values))
			for key := range values {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				_, _ = io.WriteString(h, key+"\x00"+values[key]+"\x00")
			}
			continue
		}
		f, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			_, _ = io.WriteString(h, "missing\x00")
			continue
		}
		if err != nil {
			return "", fmt.Errorf("setup: read service input %s: %w", path, err)
		}
		info, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return "", fmt.Errorf("setup: stat service input %s: %w", path, err)
		}
		_, _ = fmt.Fprintf(h, "%d\x00", info.Size())
		if _, err := io.Copy(h, f); err != nil {
			_ = f.Close()
			return "", fmt.Errorf("setup: hash service input %s: %w", path, err)
		}
		if err := f.Close(); err != nil {
			return "", fmt.Errorf("setup: close service input %s: %w", path, err)
		}
		_, _ = io.WriteString(h, "\x00")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (m *nativeServiceManager) desired(o Options, p Paths) (string, []byte, string, error) {
	addr := o.Addr
	if addr == "" {
		addr = defaultServiceAddr
	}
	if err := validateServiceAddr(addr); err != nil {
		return "", nil, "", err
	}
	path, err := m.servicePath(o)
	if err != nil {
		return "", nil, "", err
	}
	binary, err := os.Executable()
	if err != nil {
		return "", nil, "", fmt.Errorf("setup: locate rusui executable: %w", err)
	}
	values := readEnv(p.Env)
	getenv := o.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if values["PATH"] == "" {
		values["PATH"] = getenv("PATH")
	}
	if values["HOME"] == "" {
		values["HOME"] = getenv("HOME")
	}
	inputDigest, err := serviceInputsDigest(p, binary)
	if err != nil {
		return "", nil, "", err
	}
	var content []byte
	switch m.platform {
	case "launchd":
		content, err = launchdPlist(p, binary, addr, values, inputDigest)
	case "systemd":
		content, err = systemdUnit(p, binary, addr, values["PATH"], inputDigest, systemdEnvironmentPath(path))
	default:
		err = fmt.Errorf("setup: unsupported user service manager %q", m.platform)
	}
	if err != nil {
		return "", nil, "", err
	}
	return path, content, addr, nil
}

func (m *nativeServiceManager) servicePath(o Options) (string, error) {
	getenv := o.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	home := getenv("HOME")
	if home == "" {
		return "", fmt.Errorf("setup: HOME required for user service")
	}
	switch m.platform {
	case "launchd":
		return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), nil
	case "systemd":
		config := getenv("XDG_CONFIG_HOME")
		if config == "" || !filepath.IsAbs(config) {
			config = filepath.Join(home, ".config")
		}
		return filepath.Join(config, "systemd", "user", systemdUnitName), nil
	default:
		return "", fmt.Errorf("setup: user services are unsupported on %s", runtime.GOOS)
	}
}

func systemdEnvironmentPath(unitPath string) string {
	return unitPath + ".env"
}

func writeSystemdEnvironmentFile(unitPath, sourcePath string) error {
	content, err := systemdEnvironmentFile(sourcePath)
	if err != nil {
		return err
	}
	path := systemdEnvironmentPath(unitPath)
	old, err := os.ReadFile(path)
	if err == nil && bytes.Equal(old, content) {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("setup: read service environment file: %w", err)
	}
	return writeServiceFile(path, content)
}

func systemdEnvironmentFile(sourcePath string) ([]byte, error) {
	values := readEnv(sourcePath)
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(systemdEnvMarker + "\n")
	for _, key := range keys {
		value := values[key]
		if !validEnvName(key) || strings.ContainsAny(value, "\x00\n\r") {
			return nil, fmt.Errorf("setup: invalid service environment entry %q", key)
		}
		b.WriteString(key + "=" + systemdEnvironmentQuote(value) + "\n")
	}
	return []byte(b.String()), nil
}

func systemdEnvironmentQuote(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return "\"" + value + "\""
}

func managedSystemdEnvironment(content []byte) bool {
	return bytes.HasPrefix(content, []byte(systemdEnvMarker+"\n"))
}

func checkSystemdEnvironmentFile(unitPath string) error {
	path := systemdEnvironmentPath(unitPath)
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("setup: read service environment file: %w", err)
	}
	if !managedSystemdEnvironment(content) {
		return fmt.Errorf("setup: refusing to remove unmanaged service environment file at %s", path)
	}
	return nil
}

func removeSystemdEnvironmentFile(unitPath string) error {
	path := systemdEnvironmentPath(unitPath)
	if err := checkSystemdEnvironmentFile(unitPath); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("setup: remove service environment file: %w", err)
	}
	return nil
}

func (m *nativeServiceManager) lingeringEnabled() (bool, error) {
	uid := strconv.Itoa(os.Getuid())
	out, err := m.runner.Run("loginctl", "show-user", uid, "--property=Linger", "--value")
	if err != nil {
		return false, commandError("inspect systemd user lingering", err)
	}
	switch strings.TrimSpace(string(out)) {
	case "yes", "true", "1":
		return true, nil
	case "no", "false", "0":
		return false, nil
	default:
		return false, fmt.Errorf("setup: unexpected loginctl lingering status %q", strings.TrimSpace(string(out)))
	}
}

func (m *nativeServiceManager) ensureLingering() error {
	enabled, err := m.lingeringEnabled()
	if err != nil || enabled {
		return err
	}
	if _, err := m.runner.Run("loginctl", "enable-linger", strconv.Itoa(os.Getuid())); err != nil {
		return commandError("enable systemd user lingering", err)
	}
	return nil
}

func (m *nativeServiceManager) managed(content []byte) bool {
	if m.platform == "launchd" {
		return bytes.Contains(content, []byte(launchdMarker))
	}
	return m.platform == "systemd" && bytes.HasPrefix(content, []byte(systemdUnitMarker))
}

func (m *nativeServiceManager) current(o Options, path string) (bool, string, error) {
	switch m.platform {
	case "launchd":
		uid := os.Getuid()
		domain := fmt.Sprintf("gui/%d", uid)
		out, err := m.runner.Run("launchctl", "print", domain+"/"+launchdLabel)
		if err != nil {
			if isExitStatus(err) {
				return false, "", nil
			}
			return false, "", commandError("inspect launchd user service", err)
		}
		return true, launchdSource(string(out)), nil
	case "systemd":
		out, err := m.runner.Run("systemctl", "--user", "show", "-p", "FragmentPath", "--value", systemdUnitName)
		if err != nil {
			if isExitStatus(err) {
				return false, "", nil
			}
			return false, "", commandError("inspect systemd user service", err)
		}
		source := strings.TrimSpace(string(out))
		if source == "" || source == "(null)" {
			return false, "", nil
		}
		_, err = m.runner.Run("systemctl", "--user", "is-active", "--quiet", systemdUnitName)
		if err != nil && !isExitStatus(err) {
			return false, "", commandError("inspect systemd user service state", err)
		}
		return err == nil, source, nil
	default:
		return false, "", fmt.Errorf("setup: unsupported user service manager %q", m.platform)
	}
}

func (m *nativeServiceManager) activate(o Options, path string, changed, active bool) error {
	if m.platform == "launchd" {
		domain := fmt.Sprintf("gui/%d", os.Getuid())
		if active && changed {
			if _, err := m.runner.Run("launchctl", "bootout", domain, path); err != nil {
				return commandError("stop launchd user service", err)
			}
		}
		if !active || changed {
			if _, err := m.runner.Run("launchctl", "bootstrap", domain, path); err != nil {
				return commandError("start launchd user service", err)
			}
		}
		return nil
	}
	if m.platform == "systemd" {
		if _, err := m.runner.Run("systemctl", "--user", "daemon-reload"); err != nil {
			return commandError("reload systemd user units", err)
		}
		if active && changed {
			if _, err := m.runner.Run("systemctl", "--user", "restart", systemdUnitName); err != nil {
				return commandError("restart systemd user service", err)
			}
		} else if !active {
			if _, err := m.runner.Run("systemctl", "--user", "enable", "--now", systemdUnitName); err != nil {
				return commandError("enable systemd user service", err)
			}
		}
		return nil
	}
	return fmt.Errorf("setup: unsupported user service manager %q", m.platform)
}

func (m *nativeServiceManager) deactivate(o Options, path string) error {
	if m.platform == "launchd" {
		domain := fmt.Sprintf("gui/%d", os.Getuid())
		if _, err := m.runner.Run("launchctl", "bootout", domain, path); err != nil {
			return commandError("stop launchd user service", err)
		}
		return nil
	}
	if m.platform == "systemd" {
		if _, err := m.runner.Run("systemctl", "--user", "stop", systemdUnitName); err != nil {
			return commandError("stop systemd user service", err)
		}
		return nil
	}
	return fmt.Errorf("setup: unsupported user service manager %q", m.platform)
}

func validateServiceAddr(addr string) error {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("setup: service address must be host:port: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("setup: service port must be between 1 and 65535")
	}
	if host != "127.0.0.1" && host != "localhost" {
		return fmt.Errorf("setup: user service must bind to 127.0.0.1 or localhost for its generated TLS certificate")
	}
	return nil
}

func launchdSource(output string) string {
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if source, ok := strings.CutPrefix(line, "path = "); ok {
			return strings.TrimSpace(source)
		}
	}
	return ""
}

func launchdPlist(p Paths, binary, addr string, env map[string]string, inputDigest string) ([]byte, error) {
	for key, value := range env {
		if !validEnvName(key) || strings.ContainsAny(value, "\x00\n\r") {
			return nil, fmt.Errorf("setup: invalid environment value for service key %q", key)
		}
	}
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	b.WriteString(launchdMarker + "\n<!-- rusui-inputs-sha256=" + inputDigest + " -->\n<plist version=\"1.0\">\n<dict>\n")
	writePlistString(&b, "Label", launchdLabel)
	writePlistArray(&b, "ProgramArguments", []string{binary, "-addr", addr, "-db", p.DB, "-policy", p.Policy})
	writePlistString(&b, "StandardOutPath", filepath.Join(p.Dir, "rusui.log"))
	writePlistString(&b, "StandardErrorPath", filepath.Join(p.Dir, "rusui.err"))
	b.WriteString("<key>EnvironmentVariables</key>\n<dict>\n")
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		writePlistString(&b, key, env[key])
	}
	b.WriteString("</dict>\n<key>RunAtLoad</key>\n<true/>\n<key>KeepAlive</key>\n<true/>\n</dict>\n</plist>\n")
	return []byte(b.String()), nil
}

func writePlistString(b *strings.Builder, key, value string) {
	b.WriteString("<key>")
	writeXMLEscaped(b, key)
	b.WriteString("</key>\n<string>")
	writeXMLEscaped(b, value)
	b.WriteString("</string>\n")
}

func writePlistArray(b *strings.Builder, key string, values []string) {
	b.WriteString("<key>")
	writeXMLEscaped(b, key)
	b.WriteString("</key>\n<array>\n")
	for _, value := range values {
		b.WriteString("<string>")
		writeXMLEscaped(b, value)
		b.WriteString("</string>\n")
	}
	b.WriteString("</array>\n")
}

func writeXMLEscaped(b *strings.Builder, value string) {
	_ = xml.EscapeText(b, []byte(value))
}

func systemdUnit(p Paths, binary, addr, pathEnv, inputDigest, environmentFile string) ([]byte, error) {
	for _, value := range []string{environmentFile, p.Dir, binary, addr, p.DB, p.Policy, pathEnv} {
		if strings.ContainsAny(value, "\x00\n\r") {
			return nil, fmt.Errorf("setup: invalid line break in service configuration")
		}
	}
	var b strings.Builder
	b.WriteString(systemdUnitMarker + "\n# rusui-inputs-sha256=" + inputDigest + "\n")
	b.WriteString("[Unit]\nDescription=Rusui user service\nAfter=network-online.target\nWants=network-online.target\n\n")
	b.WriteString("[Service]\nType=simple\n")
	b.WriteString("WorkingDirectory=" + systemdPathValue(p.Dir) + "\n")
	b.WriteString("EnvironmentFile=" + systemdPathValue(environmentFile) + "\n")
	if pathEnv != "" {
		b.WriteString("Environment=" + systemdEnvironmentValue("PATH="+pathEnv) + "\n")
	}
	b.WriteString("ExecStart=" + strings.Join([]string{systemdUnitValue(binary), systemdExecArg("-addr"), systemdExecArg(addr), systemdExecArg("-db"), systemdExecArg(p.DB), systemdExecArg("-policy"), systemdExecArg(p.Policy)}, " ") + "\n")
	b.WriteString("Restart=on-failure\nRestartSec=5s\n\n[Install]\nWantedBy=default.target\n")
	return []byte(b.String()), nil
}

func systemdUnitValue(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	value = strings.ReplaceAll(value, "%", "%%")
	return "\"" + value + "\""
}

func systemdExecArg(value string) string {
	value = strings.ReplaceAll(value, "$", "$$")
	return systemdUnitValue(value)
}

func systemdEnvValue(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	for _, char := range []string{`"`, "$", "`"} {
		value = strings.ReplaceAll(value, char, "\\"+char)
	}
	return "\"" + value + "\""
}

func systemdEnvFileContent(raw []byte) ([]byte, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("setup: environment file must be UTF-8 for systemd")
	}
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") || !strings.Contains(line, "=") {
			lines[i] = line
			continue
		}
		key, value, ok := envLine(line)
		if !ok || !validEnvName(key) {
			return nil, fmt.Errorf("setup: invalid environment assignment for systemd")
		}
		if strings.ContainsAny(value, "\x00\n\r") {
			return nil, fmt.Errorf("setup: invalid line break in environment value for systemd")
		}
		lines[i] = key + "=" + systemdEnvValue(value)
	}
	return []byte(strings.Join(lines, "\n")), nil
}

func normalizeSystemdEnvFile(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("setup: read environment file: %w", err)
	}
	content, err := systemdEnvFileContent(raw)
	if err != nil {
		return err
	}
	if bytes.Equal(raw, content) {
		return nil
	}
	if err := writeServiceFile(path, content); err != nil {
		return fmt.Errorf("setup: normalize environment file: %w", err)
	}
	return nil
}

// systemd path directives receive one raw path value, while unit specifiers
// still expand. Double percent signs so paths containing '%' stay literal.
func systemdPathValue(value string) string {
	return strings.ReplaceAll(value, "%", "%%")
}

func systemdEnvironmentValue(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	value = strings.ReplaceAll(value, "%", "%%")
	return "\"" + value + "\""
}

func validEnvName(key string) bool {
	if key == "" || (!asciiLetter(key[0]) && key[0] != '_') {
		return false
	}
	for i := 1; i < len(key); i++ {
		ch := key[i]
		if !asciiLetter(ch) && (ch < '0' || ch > '9') && ch != '_' {
			return false
		}
	}
	return true
}

func asciiLetter(ch byte) bool {
	return ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z'
}

func writeServiceFile(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("setup: create service directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".rusui-service-*")
	if err != nil {
		return fmt.Errorf("setup: create service temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("setup: secure service file: %w", err)
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("setup: write service file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("setup: sync service file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("setup: close service file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("setup: replace service file: %w", err)
	}
	return nil
}

func commandError(action string, err error) error {
	return fmt.Errorf("setup: %s: %w", action, err)
}

func isExitStatus(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}

type unsupportedServiceManager struct{ platform string }

func (m unsupportedServiceManager) Plan(Options, Paths) (Step, error) {
	return Step{Action: NeedsYou, Item: "user service", Detail: "automatic user services are unsupported on " + m.platform}, nil
}

func (m unsupportedServiceManager) Apply(Options, Paths) (Step, error) {
	return Step{}, fmt.Errorf("setup: automatic user services are unsupported on %s", m.platform)
}

func (m unsupportedServiceManager) Remove(Options) (Step, error) {
	return Step{Action: Keep, Item: "user service", Detail: "not supported on " + m.platform}, nil
}

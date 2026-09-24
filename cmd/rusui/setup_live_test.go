package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/ops"
	"github.com/sannrox/rusui/internal/setup"
)

func TestLiveLaunchdSetupService(t *testing.T) {
	if os.Getenv("RUSUI_LIVE_SETUP_SERVICE") != "1" {
		t.Skip("set RUSUI_LIVE_SETUP_SERVICE=1 to install and probe a temporary launchd service")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("live launchd service test requires macOS")
	}
	launchctl, err := exec.LookPath("launchctl")
	if err != nil {
		t.Fatal(err)
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	if out, err := exec.Command(launchctl, "print", domain+"/com.sannrox.rusui").CombinedOutput(); err == nil || !strings.Contains(string(out), "Could not find service") {
		t.Fatalf("refusing to run unless the rusui launchd label is confirmed unloaded: err=%v\n%s", err, out)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	state := filepath.Join(root, "state")
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(launchctl, filepath.Join(binDir, "launchctl")); err != nil {
		t.Fatal(err)
	}
	paths := setup.PathsFor(state)
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Env, []byte("RUSUI_GITHUB_TOKEN=live-test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	binary := filepath.Join(root, "rusui")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build rusui: %v\n%s", err, out)
	}

	cmdEnv := []string{"HOME=" + home, "PATH=" + binDir, "RUSUI_LIVE_SETUP_SERVICE=1"}
	run := func(args ...string) ([]byte, error) {
		cmd := exec.Command(binary, args...)
		cmd.Env = cmdEnv
		return cmd.CombinedOutput()
	}
	removed := false
	t.Cleanup(func() {
		if !removed {
			out, err := run("setup", "remove-service")
			if err != nil {
				t.Errorf("remove temporary launchd service: %v\n%s", err, out)
			}
		}
	})

	applyOut, applyErr := run("setup", "apply", "-state", state, "-addr", addr)
	if applyErr != nil && exitCode(applyErr) != 1 {
		t.Fatalf("setup apply failed: %v\n%s", applyErr, applyOut)
	}
	servicePath := filepath.Join(home, "Library", "LaunchAgents", "com.sannrox.rusui.plist")
	if _, err := os.Stat(servicePath); err != nil {
		t.Fatalf("setup did not write temporary service file: %v\n%s", err, applyOut)
	}
	if strings.Contains(string(applyOut), "setup:") {
		t.Fatalf("setup reported a service installation error:\n%s", applyOut)
	}
	probeService(t, binary, cmdEnv, state, paths.CACert, addr)

	reapplyOut, reapplyErr := run("setup", "apply", "-state", state, "-addr", addr)
	if reapplyErr != nil && exitCode(reapplyErr) != 1 {
		t.Fatalf("reapply failed: %v\n%s", reapplyErr, reapplyOut)
	}
	probeService(t, binary, cmdEnv, state, paths.CACert, addr)
	if out, err := run("setup", "remove-service"); err != nil || strings.Contains(string(out), "setup:") {
		t.Fatalf("remove temporary launchd service: %v\n%s", err, out)
	}
	removed = true
	if out, err := exec.Command(launchctl, "print", domain+"/com.sannrox.rusui").CombinedOutput(); err == nil || !strings.Contains(string(out), "Could not find service") {
		t.Fatalf("temporary launchd service remained loaded: err=%v\n%s", err, out)
	}
}

func probeService(t *testing.T, binary string, cmdEnv []string, state, caFile, addr string) {
	t.Helper()
	cmdEnv = append(append([]string(nil), cmdEnv...), "RUSUI_PLANE_CA="+caFile)
	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		cmd := exec.Command(binary, "diagnose", "-policy", filepath.Join(state, "policy.yaml"), "-addr", addr, "-url", "https://"+addr)
		cmd.Env = cmdEnv
		out, err := cmd.CombinedOutput()
		if err != nil && exitCode(err) != 1 {
			lastErr = fmt.Errorf("diagnose: %w: %s", err, out)
		} else {
			var report ops.Report
			if err := json.Unmarshal(out, &report); err != nil {
				lastErr = fmt.Errorf("decode diagnose report: %w: %s", err, out)
			} else {
				for _, check := range report.Checks {
					if check.Name == "plane" && check.Status == ops.StatusReady {
						return
					}
					if check.Name == "plane" {
						lastErr = fmt.Errorf("diagnose plane status %s: %s", check.Status, check.Detail)
					}
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("rusui diagnose did not report the temporary service reachable: %v", lastErr)
}

func exitCode(err error) int {
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return exit.ExitCode()
	}
	return -1
}

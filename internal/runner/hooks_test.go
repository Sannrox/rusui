package runner

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// shellExec runs the guest command on the host with GuestHooksDir mapped to
// root, so the real install script is exercised without a container.
type shellExec struct {
	root string
	argv []string
}

func (s *shellExec) ExecStdio(_ string, argv, _ []string) (io.WriteCloser, io.ReadCloser, func(), error) {
	s.argv = argv
	args := make([]string, len(argv))
	for i, a := range argv {
		args[i] = strings.ReplaceAll(a, GuestHooksDir, s.root)
	}
	cmd := exec.Command(args[0], args[1:]...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}
	return in, out, func() { _ = cmd.Wait() }, nil
}

func TestContainerGuestReceivesCommitHooks(t *testing.T) {
	x := &shellExec{root: filepath.Join(t.TempDir(), "hooks")}
	a := &Assignment{Driver: "container", Handle: "ctr", CommitTrailers: []string{"Rusui-Session: 3"}}
	unhook, err := PrepareCommitHooks(x, a)
	if err != nil {
		t.Fatal(err)
	}
	unhook()
	if a.CommitHooksDir != GuestHooksDir {
		t.Fatalf("hooks dir %q", a.CommitHooksDir)
	}
	b, err := os.ReadFile(filepath.Join(x.root, "commit-msg"))
	if err != nil || !bytes.Equal(b, []byte(commitHook)) {
		t.Fatalf("commit-msg %q %v", b, err)
	}
	if target, err := os.Readlink(filepath.Join(x.root, "pre-push")); err != nil || target != "commit-msg" {
		t.Fatalf("pre-push link %q %v", target, err)
	}
	env := strings.Join(DriverEnv(a, "/home", "/bin"), "\n")
	if !strings.Contains(env, "core.hooksPath") || !strings.Contains(env, "RUSUI_COMMIT_TRAILERS=Rusui-Session: 3") {
		t.Fatal(env)
	}
}

type silentExec struct{}

func (silentExec) ExecStdio(string, []string, []string) (io.WriteCloser, io.ReadCloser, func(), error) {
	r, w := io.Pipe()
	_ = w.Close()
	return nopWriteCloser{}, r, func() {}, nil
}

type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }

func TestUnconfirmedGuestHookInstallFails(t *testing.T) {
	a := &Assignment{Driver: "container", Handle: "ctr", CommitTrailers: []string{"x: y"}}
	if _, err := PrepareCommitHooks(silentExec{}, a); err == nil {
		t.Fatal("expected failure when the guest does not confirm")
	}
	if a.CommitHooksDir != "" {
		t.Fatal("hooks dir set after failure")
	}
}

func TestNoTrailersNoHooks(t *testing.T) {
	a := &Assignment{}
	unhook, err := PrepareCommitHooks(nil, a)
	if err != nil {
		t.Fatal(err)
	}
	unhook()
	if strings.Contains(strings.Join(DriverEnv(a, "/h", "/bin"), "\n"), "hooksPath") {
		t.Fatal("hooks configured without trailers")
	}
}

func TestRepoHooksPathFromGlobalConfigStillRuns(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	a := &Assignment{CommitTrailers: []string{"Rusui-Session: 5"}}
	unhook, err := PrepareCommitHooks(nil, a)
	if err != nil {
		t.Fatal(err)
	}
	defer unhook()
	repoHooks := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	if err := os.WriteFile(filepath.Join(repoHooks, "pre-commit"), []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[core]\n\thooksPath = "+repoHooks+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := append(DriverEnv(a, t.TempDir(), os.Getenv("PATH")), "GIT_CONFIG_GLOBAL="+global, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=op", "GIT_AUTHOR_EMAIL=op@example.invalid", "GIT_COMMITTER_NAME=op", "GIT_COMMITTER_EMAIL=op@example.invalid")
	repo := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("init", "-q")
	run("commit", "-q", "--allow-empty", "-m", "change")
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("globally configured pre-commit hook did not run")
	}
	if msg := run("log", "-1", "--format=%B"); !strings.Contains(msg, "Rusui-Session: 5") {
		t.Fatalf("message:\n%s", msg)
	}
}

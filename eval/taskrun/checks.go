package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// checker runs held-back checks on the operator's machine, outside every
// guest, in a fresh checkout per commit.
type checker struct {
	remote  string // clone source; %s is replaced by the repository
	workDir string
	timeout time.Duration
}

// checkout fetches one commit into a new directory and applies patch.
// patched is false when the patch does not apply; the caller removes dir.
// An error means no directory is left behind.
func (k checker) checkout(ctx context.Context, repo, sha, patch string) (dir string, patched bool, err error) {
	dir, err = os.MkdirTemp(k.workDir, "taskrun-")
	if err != nil {
		return "", false, err
	}
	src := strings.ReplaceAll(k.remote, "%s", repo)
	for _, argv := range [][]string{
		{"git", "init", "-q"},
		{"git", "fetch", "-q", "--depth=1", src, sha},
		{"git", "checkout", "-q", "--detach", "FETCH_HEAD"},
	} {
		if out, err := k.run(ctx, dir, "", argv); err != nil {
			k.remove(dir)
			return "", false, fmt.Errorf("%s %s: %w: %s", argv[0], argv[1], err, out)
		}
	}
	if patch != "" {
		if _, err := k.run(ctx, dir, patch, []string{"git", "apply", "-"}); err != nil {
			return dir, false, nil
		}
	}
	return dir, true, nil
}

// passes reports whether every command exits zero in dir.
func (k checker) passes(ctx context.Context, dir string, cmds [][]string) bool {
	for _, argv := range cmds {
		if _, err := k.run(ctx, dir, "", argv); err != nil {
			return false
		}
	}
	return true
}

func (k checker) run(ctx context.Context, dir, stdin string, argv []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, k.timeout)
	defer cancel()
	// Output goes to a file, not a pipe, so the check ends when its own
	// process exits even if a child it left behind still holds stdout.
	out, err := os.CreateTemp(k.workDir, "output-")
	if err != nil {
		return "", err
	}
	defer func() { _ = out.Close(); _ = os.Remove(out.Name()) }()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.Env = checkEnv(dir)
	// The check runs in its own process group: a timeout kills all of it,
	// and so does the check's exit, so nothing it started outlives it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if err := cmd.Start(); err != nil {
		return "", err
	}
	err = cmd.Wait()
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	b, _ := os.ReadFile(out.Name())
	return string(b), err
}

// checkEnv is the whole environment of a command run in checkout dir.
// Checks run code the agent wrote, so they get an empty HOME and TMPDIR
// beside the checkout and never the operator's plane tokens, forge
// credentials, or Git config. This is not a sandbox: the process still
// runs as the operator (see eval/set.md).
func checkEnv(dir string) []string {
	scratch := dir + ".env"
	_ = os.MkdirAll(filepath.Join(scratch, "tmp"), 0o700)
	env := []string{
		"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"HOME=" + scratch, "TMPDIR=" + filepath.Join(scratch, "tmp"),
	}
	for _, k := range []string{"PATH", "LANG"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// verdict holds what the checks showed for one published head.
type verdict struct {
	headPass, basePass, gatePass bool
}

// score checks one task: the head with the test patch, the base with it
// (a check that already passes there proves nothing), and the
// repository gate on the unpatched head. A head the test patch does not
// apply to fails its checks; a base it does not apply to is a corpus
// error.
func (k checker) score(ctx context.Context, c *Corpus, t Task, head string) (verdict, error) {
	var v verdict
	ok, err := k.inCheckout(ctx, c.Repo, head, t.TestPatch, t.Checks)
	if err != nil || !ok {
		return v, err
	}
	v.headPass = true
	dir, applied, err := k.checkout(ctx, c.Repo, t.BaseSHA, t.TestPatch)
	if err != nil {
		return v, err
	}
	if applied {
		// Each check must fail on the base; one that passes proves nothing.
		for _, argv := range t.Checks {
			v.basePass = v.basePass || k.passes(ctx, dir, [][]string{argv})
		}
	}
	k.remove(dir)
	if !applied {
		return v, fmt.Errorf("task %s: test patch does not apply to its base", t.ID)
	}
	if len(c.Gate) == 0 {
		v.gatePass = true
		return v, nil
	}
	v.gatePass, err = k.inCheckout(ctx, c.Repo, head, "", [][]string{c.Gate})
	return v, err
}

// inCheckout runs cmds in a fresh checkout of sha with patch applied and
// reports whether all of them passed.
func (k checker) inCheckout(ctx context.Context, repo, sha, patch string, cmds [][]string) (bool, error) {
	dir, applied, err := k.checkout(ctx, repo, sha, patch)
	if err != nil {
		return false, err
	}
	defer k.remove(dir)
	return applied && k.passes(ctx, dir, cmds), nil
}

// remove deletes a checkout and its scratch environment.
func (k checker) remove(dir string) {
	removeTree(dir)
	removeTree(dir + ".env")
}

// removeTree deletes path even when a check left read-only directories
// behind, as Go's module cache does.
func removeTree(path string) {
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	_ = os.RemoveAll(path)
}

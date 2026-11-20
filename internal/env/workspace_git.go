package env

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitDiffScript prints git status and a diff against HEAD (the snapshot
// pin) from the real /workspace. Exit 13 is a replaced root (#447); 14 is
// no git pin; 15 is a guest without git.
const gitDiffScript = `cd -P /workspace 2>/dev/null && [ "$(pwd -P)" = /workspace ] || exit 13
[ -e .git ] || exit 14
command -v git >/dev/null 2>&1 || exit 15
git -c safe.directory=/workspace rev-parse --verify HEAD >/dev/null 2>&1 || exit 14
git -c safe.directory=/workspace --no-pager status --porcelain=v1 --untracked-files=all
printf '\n'
git -c safe.directory=/workspace --no-pager diff --no-color HEAD || true
`

// WorkspaceGitDiff is git status and git diff against HEAD in one exec.
func (d DockerCLI) WorkspaceGitDiff(id string, cap int64) ([]byte, string, error) {
	cmd := exec.Command(d.bin(), "exec", id, "sh", "-c", gitDiffScript)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, "", err
	}
	if err := cmd.Start(); err != nil {
		return nil, "", err
	}
	limit := max(cap+1, 1)
	raw, readErr := io.ReadAll(io.LimitReader(stdout, limit))
	_ = stdout.Close()
	waitErr := cmd.Wait()
	if ee, ok := errors.AsType[*exec.ExitError](waitErr); ok {
		switch ee.ExitCode() {
		case 13:
			return nil, "", ErrWorkspaceReplaced
		case 14:
			return nil, FileNoGitPin, nil
		case 15:
			return nil, "", fmt.Errorf("env: git missing")
		}
	}
	if readErr != nil {
		return nil, "", readErr
	}
	if waitErr != nil && (cap < 0 || int64(len(raw)) < limit) {
		return nil, "", fmt.Errorf("env: git diff: %w", waitErr)
	}
	return interpretGitDiff(raw, cap)
}

// GitDiff is git status and git diff against HEAD in a host workspace.
func GitDiff(dir string, cap int64) ([]byte, string, error) {
	if dir == "" {
		return nil, "", fmt.Errorf("env: empty handle")
	}
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err != nil {
		return nil, FileNoGitPin, nil
	}
	if _, err := gitOutput(dir, "rev-parse", "--verify", "HEAD"); err != nil {
		return nil, FileNoGitPin, nil
	}
	status, err := gitOutput(dir, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return nil, "", err
	}
	diff, err := gitOutput(dir, "--no-pager", "diff", "--no-color", "HEAD")
	if err != nil {
		return nil, "", err
	}
	var b bytes.Buffer
	b.Write(status)
	if len(status) > 0 && !bytes.HasSuffix(status, []byte("\n")) {
		b.WriteByte('\n')
	}
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	b.Write(diff)
	return interpretGitDiff(b.Bytes(), cap)
}

func interpretGitDiff(raw []byte, cap int64) ([]byte, string, error) {
	truncated := cap >= 0 && int64(len(raw)) > cap
	if truncated {
		raw = raw[:cap]
	}
	if !truncated && len(bytes.TrimSpace(raw)) == 0 {
		return nil, FileClean, nil
	}
	if truncated {
		raw = append(append([]byte{}, raw...), []byte("\n# "+FileSkipped+"\n")...)
	}
	return raw, "", nil
}

func gitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-c", "safe.directory=*"}, args...)...)
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + dir,
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	}
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok && ee.ExitCode() == 1 {
			return out, nil
		}
		return nil, fmt.Errorf("env: git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

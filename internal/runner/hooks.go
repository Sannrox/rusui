package runner

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// GuestHooksDir holds the attribution hooks inside a container guest.
const GuestHooksDir = "/tmp/rusui-hooks"

// delegatedHooks are the other client-side git hooks, linked to the
// commit-msg script. Every name delegates to the repository's own hook, so
// pointing core.hooksPath at the rusui directory does not disable them.
var delegatedHooks = []string{
	"applypatch-msg", "pre-applypatch", "post-applypatch", "pre-commit",
	"pre-merge-commit", "prepare-commit-msg", "post-commit",
	"pre-rebase", "post-checkout", "post-merge", "pre-push", "post-rewrite",
	"reference-transaction", "pre-auto-gc", "post-index-change",
}

// commitHook runs the repository's hook of the same name (its own
// core.hooksPath from any scope but the command-line one rusui sets, else
// .git/hooks), then, for commit-msg, adds each line of
// RUSUI_COMMIT_TRAILERS unless an identical trailer is already present
// (ADR 0015 D3).
const commitHook = `#!/bin/sh
name=$(basename "$0")
hooks=$(git config --show-scope --get-all core.hooksPath 2>/dev/null | awk -F '\t' '$1 != "command" { v = $2 } END { print v }')
[ -n "$hooks" ] || hooks="$(git rev-parse --git-common-dir)/hooks"
if [ -x "$hooks/$name" ]; then "$hooks/$name" "$@" || exit $?; fi
[ "$name" = commit-msg ] || exit 0
while IFS= read -r t; do
	[ -n "$t" ] || continue
	git interpret-trailers --in-place --if-exists addIfDifferent --trailer "$t" "$1" || exit 1
done <<EOF
$RUSUI_COMMIT_TRAILERS
EOF
`

// PrepareCommitHooks installs the attribution hooks for a turn that carries
// commit trailers and records their directory on the assignment. Container
// guests receive them through x; other guests get a host temp directory,
// which the returned cleanup removes.
func PrepareCommitHooks(x StdioExec, a *Assignment) (func(), error) {
	if len(a.CommitTrailers) == 0 {
		return func() {}, nil
	}
	if a.Driver == "container" && a.Handle != "" {
		if x == nil {
			return nil, fmt.Errorf("container exec required for commit hooks")
		}
		if err := installGuestHooks(x, a.Handle); err != nil {
			return nil, err
		}
		a.CommitHooksDir = GuestHooksDir
		return func() {}, nil
	}
	dir, err := os.MkdirTemp("", "rusui-hooks-*")
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := os.WriteFile(filepath.Join(dir, "commit-msg"), []byte(commitHook), 0o755); err != nil {
		cleanup()
		return nil, err
	}
	for _, n := range delegatedHooks {
		if err := os.Symlink("commit-msg", filepath.Join(dir, n)); err != nil {
			cleanup()
			return nil, err
		}
	}
	a.CommitHooksDir = dir
	return cleanup, nil
}

func installGuestHooks(x StdioExec, handle string) error {
	d := GuestHooksDir
	script := fmt.Sprintf("mkdir -p %[1]s && cat > %[1]s/commit-msg && chmod 755 %[1]s/commit-msg && for n in %[2]s; do ln -sf commit-msg %[1]s/$n || exit 1; done && echo ok",
		d, strings.Join(delegatedHooks, " "))
	stdin, stdout, stop, err := x.ExecStdio(handle, []string{"/bin/sh", "-c", script}, nil)
	if err != nil {
		return fmt.Errorf("install commit hooks: %w", err)
	}
	defer stop()
	if _, err := io.WriteString(stdin, commitHook); err != nil {
		return fmt.Errorf("install commit hooks: %w", err)
	}
	if err := stdin.Close(); err != nil {
		return fmt.Errorf("install commit hooks: %w", err)
	}
	out, _ := io.ReadAll(stdout)
	if strings.TrimSpace(string(out)) != "ok" {
		return fmt.Errorf("install commit hooks: guest did not confirm")
	}
	return nil
}

package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/guest"
)

// prepareGuestHome owns temporary guest state separately from the checkout.
// Codex repository config is refused because CODEX_HOME alone does not
// disable its project configuration layers.
func prepareGuestHome(x StdioExec, a *Assignment, workspace string) (string, func(), error) {
	if a.Driver == "container" && a.Handle != "" {
		script := `if [ "$1" = codex ] && [ -e .codex ]; then printf 'repo-config'; exit 1; fi
umask 077
mktemp -d /tmp/rusui-guest-XXXXXXXXXX`
		out, err := guestOutput(x, a, "", "sh", "-c", script, "rusui-guest-home", a.Guest)
		home := strings.TrimSpace(string(out))
		if err != nil || !strings.HasPrefix(home, "/tmp/rusui-guest-") || filepath.Clean(home) != home || strings.ContainsAny(home, "\r\n") || strings.Contains(home[len("/tmp/"):], "/") {
			return "", nil, fmt.Errorf("guest home: refused or unconfirmed container directory")
		}
		return home, func() { _, _ = guestOutput(x, a, "", "rm", "-rf", "--", home) }, nil
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", nil, err
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", nil, err
	}
	if a.Guest == guest.KindCodex {
		dir := workspace
		for {
			if _, err := os.Lstat(filepath.Join(dir, ".codex")); err == nil {
				return "", nil, fmt.Errorf("guest home: repository Codex config refused")
			} else if !os.IsNotExist(err) {
				return "", nil, err
			}
			if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
				break
			} else if !os.IsNotExist(err) {
				return "", nil, err
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	home, err := os.MkdirTemp("", "rusui-guest-*")
	if err != nil {
		return "", nil, err
	}
	clean := func() { _ = os.RemoveAll(home) }
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		clean()
		return "", nil, err
	}
	rel, err := filepath.Rel(workspace, realHome)
	if err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
		clean()
		return "", nil, fmt.Errorf("guest home: temporary directory must be outside workspace")
	}
	return realHome, clean, nil
}

// sessionStateDir creates or reuses the session's guest conversation store,
// kept between turns so a follow-up turn can load the stored conversation.
// It holds no configuration the guest executes: the plane passes that per
// turn. It lives beside a process workspace or in the container's /tmp and
// is removed with the environment. A turn without a session environment
// gets "" and keeps its state in the temporary home.
func sessionStateDir(x StdioExec, a *Assignment) (string, error) {
	if a.SessionID < 1 {
		return "", nil
	}
	session := strconv.FormatInt(a.SessionID, 10)
	if a.Driver == "container" && a.Handle != "" {
		want := "/tmp/rusui-state-s" + session
		out, err := guestOutput(x, a, "", "sh", "-c", containerStateScript, "rusui-guest-state", want)
		if err != nil || string(out) != want {
			return "", fmt.Errorf("guest state: refused or unconfirmed container directory")
		}
		return want, nil
	}
	if a.Workspace == "" {
		return "", nil
	}
	root := env.GuestStateRoot(a.Workspace)
	dir := filepath.Join(root, session)
	for _, d := range []string{root, dir} {
		if err := os.Mkdir(d, 0o700); err != nil && !os.IsExist(err) {
			return "", err
		}
		fi, err := os.Lstat(d)
		if err != nil {
			return "", err
		}
		if !fi.IsDir() || fi.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("guest state: refused session directory")
		}
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	realWorkspace, err := filepath.EvalSymlinks(a.Workspace)
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(realWorkspace, realDir); err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
		return "", fmt.Errorf("guest state: directory must be outside workspace")
	}
	return realDir, nil
}

// containerStateScript creates or reuses $1 and prints it, refusing a link
// or a directory the guest user does not own.
const containerStateScript = `umask 077
if [ -L "$1" ] || { [ -e "$1" ] && { [ ! -d "$1" ] || [ ! -O "$1" ]; }; }; then exit 1; fi
mkdir -p "$1" && chmod 700 "$1" && printf '%s' "$1"`

// guestHomeArgv adapts argv to the guest home; state is shikigami's
// conversation store.
func guestHomeArgv(a *Assignment, argv []string, state string) []string {
	argv = append([]string(nil), argv...)
	switch a.Guest {
	case guest.KindClaude:
		// Load only the fresh home and the plane's explicit settings.
		argv = append(argv, "--setting-sources", "user", "--strict-mcp-config")
	case guest.KindShikigami:
		for i := 0; i+1 < len(argv); i++ {
			if argv[i] == "--state" {
				argv[i+1] = state
				return argv
			}
		}
		argv = append(argv, "--state", state)
	}
	return argv
}

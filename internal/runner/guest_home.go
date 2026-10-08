package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

func guestHomeArgv(a *Assignment, argv []string, home string) []string {
	argv = append([]string(nil), argv...)
	switch a.Guest {
	case guest.KindClaude:
		// Load only the fresh home and the plane's explicit settings.
		argv = append(argv, "--setting-sources", "user", "--strict-mcp-config")
	case guest.KindShikigami:
		for i := 0; i+1 < len(argv); i++ {
			if argv[i] == "--state" {
				argv[i+1] = filepath.Join(home, "state")
				return argv
			}
		}
		argv = append(argv, "--state", filepath.Join(home, "state"))
	}
	return argv
}

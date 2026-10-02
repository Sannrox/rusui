package env

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Workspace file states a container inspection reports instead of a body.
const (
	FileMissing    = "missing"
	FileIrregular  = "not a regular file"
	FileOversized  = "oversized"
	FilePathDenied = "path"
	// FileRootReplaced is a /workspace that is no longer the real
	// workspace directory.
	FileRootReplaced = "workspace replaced"
)

// ErrWorkspaceReplaced is a /workspace that is no longer a real
// directory, such as a symlink the guest put in its place (#447).
var ErrWorkspaceReplaced = errors.New("env: workspace replaced")

// workspaceRoot enters /workspace and refuses (exit 13) unless it is the
// real directory: a guest that replaces /workspace with a symlink, for
// example to /etc, must not make other files look like its workspace
// (#447). Reads after it are relative to that directory, so swapping the
// name afterwards does not redirect them.
//
// listWorkspaceScript prints every top-level workspace entry that is not
// a directory, NUL-separated, like os.ReadDir without the directories.
const listWorkspaceScript = `cd -P /workspace 2>/dev/null && [ "$(pwd -P)" = /workspace ] || exit 13; for f in * .[!.]* ..?*; do [ -e "$f" ] || [ -L "$f" ] || continue; if [ -d "$f" ] && [ ! -L "$f" ]; then continue; fi; printf '%s\0' "$f"; done`

// readWorkspaceScript prints one top-level workspace file. The name is
// $1 and the size cap $2, never part of the script text. The read is
// bounded even if the guest swaps the file after the checks.
const readWorkspaceScript = `cd -P /workspace 2>/dev/null && [ "$(pwd -P)" = /workspace ] || exit 13; f="$1"; [ -L "$f" ] && exit 10; [ -e "$f" ] || exit 11; [ -f "$f" ] || exit 10; [ "$(wc -c < "$f")" -gt "$2" ] && exit 12; head -c "$(($2 + 1))" -- "$f"`

// WorkspaceNames lists the top-level non-directory entries of a guest's
// workspace (#440).
func (d DockerCLI) WorkspaceNames(id string) ([]string, error) {
	out, err := exec.Command(d.bin(), "exec", id, "sh", "-c", listWorkspaceScript).Output()
	if ee, ok := errors.AsType[*exec.ExitError](err); ok && ee.ExitCode() == 13 {
		return nil, ErrWorkspaceReplaced
	}
	if err != nil {
		return nil, fmt.Errorf("env: list workspace: %w", err)
	}
	var names []string
	for n := range strings.SplitSeq(string(out), "\x00") {
		if n != "" {
			names = append(names, n)
		}
	}
	return names, nil
}

// WorkspaceFile reads one top-level workspace file of at most limit
// bytes. A file it will not return has a state instead: missing, not a
// regular file (including any symlink), oversized, or a denied path.
func (d DockerCLI) WorkspaceFile(id, name string, limit int64) ([]byte, string, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return nil, FilePathDenied, nil
	}
	out, err := exec.Command(d.bin(), "exec", id, "sh", "-c", readWorkspaceScript, "sh", name, strconv.FormatInt(limit, 10)).Output()
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		switch ee.ExitCode() {
		case 10:
			return nil, FileIrregular, nil
		case 11:
			return nil, FileMissing, nil
		case 12:
			return nil, FileOversized, nil
		case 13:
			return nil, FileRootReplaced, nil
		}
	}
	if err != nil {
		return nil, "", fmt.Errorf("env: read workspace file: %w", err)
	}
	if int64(len(out)) > limit {
		return nil, FileOversized, nil
	}
	return out, "", nil
}

// WorkspaceInspector is a runtime that can list and read a guest's
// workspace without touching the host filesystem.
type WorkspaceInspector interface {
	WorkspaceNames(id string) ([]string, error)
	WorkspaceFile(id, name string, limit int64) ([]byte, string, error)
}

// Inspector returns the container runtime's workspace inspector.
func (c Container) Inspector() (WorkspaceInspector, bool) {
	w, ok := c.RT.(WorkspaceInspector)
	return w, ok
}

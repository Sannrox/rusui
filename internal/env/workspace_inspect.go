package env

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
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
	// FileSkipped is a file left out once a snapshot reached its total cap.
	FileSkipped = "skipped: workspace diff too large"
	// FileNoGitPin is a workspace with no git pin to diff against.
	FileNoGitPin = "no git pin"
	// FileClean is a git workspace whose tree matches HEAD (the pin).
	FileClean = "clean"
)

var (
	// ErrWorkspaceReplaced is a /workspace that is no longer a real
	// directory, such as a symlink the guest put in its place (#447).
	ErrWorkspaceReplaced = errors.New("env: workspace replaced")
	// ErrWorkspacePath is a workspace-relative path that escapes the
	// workspace or is otherwise refused.
	ErrWorkspacePath = errors.New("env: path")
	// ErrWorkspaceMissing is a list or read target that does not exist.
	ErrWorkspaceMissing = errors.New("env: missing")
	// ErrWorkspaceNotDir is a list target that is not a real directory
	// (a file or any symlink).
	ErrWorkspaceNotDir = errors.New("env: not a directory")
	// ErrWorkspaceNotFile is a read target that is not a regular file.
	ErrWorkspaceNotFile = errors.New("env: not a file")
	// ErrWorkspaceOversized is a read larger than the cap.
	ErrWorkspaceOversized = errors.New("env: oversized")
)

// workspaceRoot enters /workspace and refuses (exit 13) unless it is the
// real directory: a guest that replaces /workspace with a symlink, for
// example to /etc, must not make other files look like its workspace
// (#447). Reads after it are relative to that directory, so swapping the
// name afterwards does not redirect them.
//
// listWorkspaceScript prints every top-level workspace entry that is not
// a directory, NUL-separated, like os.ReadDir without the directories.
const listWorkspaceScript = `cd -P /workspace 2>/dev/null && [ "$(pwd -P)" = /workspace ] || exit 13; for f in * .[!.]* ..?*; do [ -e "$f" ] || [ -L "$f" ] || continue; if [ -d "$f" ] && [ ! -L "$f" ]; then continue; fi; printf '%s\0' "$f"; done`

// listWorkspaceDirScript lists one directory of the real /workspace,
// including directories, NUL-separated. $1 is a relative directory or
// empty for the top level. Intermediate components and the target must
// be real directories, not symlinks (exit 10); a missing target is 11;
// ".." is 14. The path is never interpolated into the script.
const listWorkspaceDirScript = `cd -P /workspace 2>/dev/null && [ "$(pwd -P)" = /workspace ] || exit 13
rest=$1
while [ -n "$rest" ]; do
  part=${rest%%/*}
  if [ "$part" = "$rest" ]; then
    rest=
  else
    rest=${rest#*/}
  fi
  if [ -z "$part" ] || [ "$part" = "." ]; then
    continue
  fi
  [ "$part" = ".." ] && exit 14
  [ -L "$part" ] && exit 10
  [ -d "$part" ] || { [ -e "$part" ] && exit 10; exit 11; }
  cd -- "$part" || exit 10
done
for f in * .[!.]* ..?*; do
  [ -e "$f" ] || [ -L "$f" ] || continue
  printf '%s\0' "$f"
done`

// readWorkspaceScript prints one workspace file. $1 is a relative path
// or a top-level name; $2 is the size cap. Intermediate components must
// be real directories, not symlinks. A symlink anywhere is 14; a
// missing target is 11; a non-file is 10; oversized is 12. The path is
// never interpolated into the script. The read is bounded even if the
// guest swaps the file after the checks.
const readWorkspaceScript = `cd -P /workspace 2>/dev/null && [ "$(pwd -P)" = /workspace ] || exit 13
rest=$1
limit=$2
[ -z "$rest" ] && exit 10
while [ -n "$rest" ]; do
  part=${rest%%/*}
  if [ "$part" = "$rest" ]; then
    rest=
    last=1
  else
    rest=${rest#*/}
    last=0
  fi
  if [ -z "$part" ] || [ "$part" = "." ]; then
    continue
  fi
  [ "$part" = ".." ] && exit 14
  [ -L "$part" ] && exit 14
  if [ "$last" = 1 ]; then
    [ -e "$part" ] || exit 11
    [ -f "$part" ] || exit 10
    [ "$(wc -c < "$part")" -gt "$limit" ] && exit 12
    head -c "$(($limit + 1))" -- "$part"
    exit 0
  fi
  [ -d "$part" ] || { [ -e "$part" ] && exit 10; exit 11; }
  cd -- "$part" || exit 10
done
exit 11`

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

func workspaceListRel(rel string) (string, error) {
	if rel == "" || rel == "." {
		return "", nil
	}
	clean, ok := SafeWorkspaceRel(rel)
	if !ok {
		return "", ErrWorkspacePath
	}
	return clean, nil
}

func parseWorkspaceList(out []byte) []string {
	var names []string
	for n := range strings.SplitSeq(string(out), "\x00") {
		if n != "" {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	return names
}

func mapListExit(err error) error {
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		switch ee.ExitCode() {
		case 10:
			return ErrWorkspaceNotDir
		case 11:
			return ErrWorkspaceMissing
		case 13:
			return ErrWorkspaceReplaced
		case 14:
			return ErrWorkspacePath
		}
	}
	return nil
}

func mapUnixListErr(err error) error {
	switch {
	case errors.Is(err, unix.ENOENT):
		return ErrWorkspaceMissing
	case errors.Is(err, unix.ENOTDIR), errors.Is(err, unix.ELOOP):
		return ErrWorkspaceNotDir
	default:
		return fmt.Errorf("env: list workspace: %w", err)
	}
}

// WorkspaceList lists one directory of a guest workspace, including
// directories. Empty rel is the top level. It does not follow
// symlinks and refuses a replaced /workspace.
func (d DockerCLI) WorkspaceList(id, rel string) ([]string, error) {
	clean, err := workspaceListRel(rel)
	if err != nil {
		return nil, err
	}
	out, err := exec.Command(d.bin(), "exec", id, "sh", "-c", listWorkspaceDirScript, "sh", clean).Output()
	if mapped := mapListExit(err); mapped != nil {
		return nil, mapped
	}
	if err != nil {
		return nil, fmt.Errorf("env: list workspace: %w", err)
	}
	return parseWorkspaceList(out), nil
}

// ListWorkspace lists one directory of a process workspace, including
// directories. Empty rel is the top level. It walks with O_NOFOLLOW.
func (Process) ListWorkspace(handle, rel string) ([]string, error) {
	if handle == "" {
		return nil, fmt.Errorf("env: workspace missing")
	}
	clean, err := workspaceListRel(rel)
	if err != nil {
		return nil, err
	}
	dirfd, err := unix.Open(handle, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, mapUnixListErr(err)
	}
	if clean != "" {
		for part := range strings.SplitSeq(clean, "/") {
			if part == "" || part == "." {
				continue
			}
			next, err := unix.Openat(dirfd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			_ = unix.Close(dirfd)
			if err != nil {
				return nil, mapUnixListErr(err)
			}
			dirfd = next
		}
	}
	f := os.NewFile(uintptr(dirfd), handle)
	defer func() { _ = f.Close() }()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, fmt.Errorf("env: list workspace: %w", err)
	}
	slices.Sort(names)
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
		case 14:
			return nil, FileIrregular, nil
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
	WorkspaceList(id, rel string) ([]string, error)
	WorkspaceFile(id, name string, limit int64) ([]byte, string, error)
	WorkspaceSnapshot(id string, fileCap, totalCap int64) ([]WorkspaceEntry, error)
	WorkspaceGitDiff(id string, cap int64) ([]byte, string, error)
}

// Inspector returns the container runtime's workspace inspector.
func (c Container) Inspector() (WorkspaceInspector, bool) {
	w, ok := c.RT.(WorkspaceInspector)
	return w, ok
}

// WorkspaceEntry is one top-level workspace file in a snapshot: its body,
// or the state that stands in for it.
type WorkspaceEntry struct {
	Name  string
	Body  []byte
	State string
}

// snapshotWorkspaceScript prints every top-level non-directory entry of
// the real /workspace as NUL-separated triples: base64 name, state, and
// base64 of at most $1+1 bytes of body. One exec covers the whole
// workspace (#449); base64 keeps names and bodies from breaking the
// framing.
const snapshotWorkspaceScript = `cd -P /workspace 2>/dev/null && [ "$(pwd -P)" = /workspace ] || exit 13; for f in * .[!.]* ..?*; do [ -e "$f" ] || [ -L "$f" ] || continue; if [ -d "$f" ] && [ ! -L "$f" ]; then continue; fi; printf '%s' "$f" | base64; printf '\0'; if [ -L "$f" ] || [ ! -f "$f" ]; then printf 'irregular\0\0'; continue; fi; printf 'file\0'; head -c "$(($1 + 1))" -- "$f" | base64; printf '\0'; done`

// WorkspaceSnapshot reads every top-level workspace file in one exec. A
// file over fileCap is oversized; once the bodies reach totalCap, the
// remaining files are reported as skipped.
func (d DockerCLI) WorkspaceSnapshot(id string, fileCap, totalCap int64) ([]WorkspaceEntry, error) {
	cmd := exec.Command(d.bin(), "exec", id, "sh", "-c", snapshotWorkspaceScript, "sh", strconv.FormatInt(fileCap, 10))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// base64 is 4/3 of the bodies, plus names and framing.
	limit := totalCap*4/3 + fileCap*4/3 + 1<<20
	raw, readErr := io.ReadAll(io.LimitReader(stdout, limit))
	_ = stdout.Close()
	waitErr := cmd.Wait()
	if ee, ok := errors.AsType[*exec.ExitError](waitErr); ok && ee.ExitCode() == 13 {
		return nil, ErrWorkspaceReplaced
	}
	if readErr != nil {
		return nil, readErr
	}
	truncated := int64(len(raw)) >= limit
	if waitErr != nil && !truncated {
		return nil, fmt.Errorf("env: snapshot workspace: %w", waitErr)
	}
	return parseWorkspaceSnapshot(raw, fileCap, totalCap, truncated)
}

func parseWorkspaceSnapshot(raw []byte, fileCap, totalCap int64, truncated bool) ([]WorkspaceEntry, error) {
	fields := strings.Split(string(raw), "\x00")
	decode := func(s string) ([]byte, error) {
		return base64.StdEncoding.DecodeString(strings.NewReplacer("\n", "", "\r", "").Replace(s))
	}
	var out []WorkspaceEntry
	var total int64
	for i := 0; i+2 < len(fields); i += 3 {
		name, err := decode(fields[i])
		if err != nil {
			return nil, fmt.Errorf("env: snapshot name: %w", err)
		}
		e := WorkspaceEntry{Name: string(name)}
		switch {
		case fields[i+1] == "irregular":
			e.State = FileIrregular
		case fields[i+1] != "file":
			return nil, fmt.Errorf("env: snapshot state %q", fields[i+1])
		case total >= totalCap:
			e.State = FileSkipped
		default:
			body, err := decode(fields[i+2])
			if err != nil {
				return nil, fmt.Errorf("env: snapshot body: %w", err)
			}
			if int64(len(body)) > fileCap {
				e.State = FileOversized
			} else {
				e.Body = body
				total += int64(len(body))
			}
		}
		out = append(out, e)
	}
	if truncated {
		out = append(out, WorkspaceEntry{Name: "…", State: FileSkipped})
	}
	return out, nil
}

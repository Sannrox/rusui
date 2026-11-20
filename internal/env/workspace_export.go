package env

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// Workspace export tar members. tree/ is the working tree without .git,
// so a guest config or credential never leaves the environment.
const (
	exportHeadName   = "head"
	exportBundleName = "bundle"
	exportTreePrefix = "tree/"
)

// ErrWorkspaceNoGit is a workspace with no git HEAD to export.
var ErrWorkspaceNoGit = errors.New("env: no git workspace")

// ExportEntry is one working-tree path in a workspace export.
type ExportEntry struct {
	Body []byte
	Link string
	Mode fs.FileMode
}

// WorkspaceHEAD is the git HEAD of a workspace directory.
func WorkspaceHEAD(srcDir string) (string, error) {
	head, err := gitStrict(srcDir, "rev-parse", "HEAD")
	if err != nil {
		return "", ErrWorkspaceNoGit
	}
	head = bytes.TrimSpace(head)
	if len(head) == 0 {
		return "", ErrWorkspaceNoGit
	}
	return string(head), nil
}

// WriteWorkspaceExport writes a tar of srcDir's git HEAD plus the
// working tree except .git. It writes nothing when HEAD is missing.
func WriteWorkspaceExport(srcDir string, w io.Writer) error {
	head, err := WorkspaceHEAD(srcDir)
	if err != nil {
		return err
	}
	bundle, err := gitStrict(srcDir, "bundle", "create", "-", "HEAD")
	if err != nil {
		return fmt.Errorf("env: git bundle: %w", err)
	}

	tw := tar.NewWriter(w)
	defer func() { _ = tw.Close() }()
	if err := tarAdd(tw, exportHeadName, append([]byte(head), '\n')); err != nil {
		return err
	}
	if err := tarAdd(tw, exportBundleName, bundle); err != nil {
		return err
	}
	return filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if skipExportPath(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		name := exportTreePrefix + rel
		if d.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return tw.WriteHeader(&tar.Header{Typeflag: tar.TypeSymlink, Name: name, Linkname: target, Mode: 0o644})
		}
		if d.IsDir() {
			return tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: name + "/", Mode: 0o700})
		}
		if !d.Type().IsRegular() {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		mode := int64(info.Mode().Perm())
		if mode == 0 {
			mode = 0o600
		}
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Size: int64(len(body)), Mode: mode}); err != nil {
			return err
		}
		_, err = tw.Write(body)
		return err
	})
}

// ReadWorkspaceExport parses a tar written by WriteWorkspaceExport.
func ReadWorkspaceExport(r io.Reader) (head string, bundle []byte, files map[string]ExportEntry, err error) {
	files = map[string]ExportEntry{}
	tr := tar.NewReader(r)
	for {
		hdr, readErr := tr.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", nil, nil, fmt.Errorf("env: export tar: %w", readErr)
		}
		name := path.Clean("/" + strings.ReplaceAll(hdr.Name, "\\", "/"))
		name = strings.TrimPrefix(name, "/")
		switch {
		case name == exportHeadName:
			body, err := io.ReadAll(tr)
			if err != nil {
				return "", nil, nil, err
			}
			head = string(bytes.TrimSpace(body))
		case name == exportBundleName:
			bundle, err = io.ReadAll(tr)
			if err != nil {
				return "", nil, nil, err
			}
		case strings.HasPrefix(name, exportTreePrefix):
			rel := strings.TrimPrefix(name, exportTreePrefix)
			rel = strings.TrimSuffix(rel, "/")
			if rel == "" || rel == "." || !fs.ValidPath(rel) || skipExportPath(rel) {
				continue
			}
			ent := ExportEntry{Mode: fs.FileMode(hdr.Mode).Perm()}
			switch hdr.Typeflag {
			case tar.TypeSymlink:
				ent.Link = hdr.Linkname
			case tar.TypeDir:
				continue
			default:
				body, err := io.ReadAll(tr)
				if err != nil {
					return "", nil, nil, err
				}
				ent.Body = body
			}
			if ent.Mode == 0 {
				ent.Mode = 0o600
			}
			files[rel] = ent
		}
	}
	if head == "" || len(bundle) == 0 {
		return "", nil, nil, fmt.Errorf("env: incomplete workspace export")
	}
	return head, bundle, files, nil
}

func skipExportPath(rel string) bool {
	for part := range strings.SplitSeq(rel, "/") {
		switch part {
		case ".git", ".rusui-snapshot", ".rusui-env":
			return true
		}
	}
	return false
}

func tarAdd(tw *tar.Writer, name string, body []byte) error {
	hdr := &tar.Header{Typeflag: tar.TypeReg, Name: name, Size: int64(len(body)), Mode: 0o600}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(body)
	return err
}

func gitStrict(dir string, args ...string) ([]byte, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	cmd := exec.Command("git", append([]string{"-c", "safe.directory=*"}, args...)...)
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + dir,
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CEILING_DIRECTORIES=" + filepath.Dir(abs),
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("env: git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

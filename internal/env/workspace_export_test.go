package env

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteWorkspaceExportHeadBundleAndDirtyTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	src := initGitWorkspace(t)
	if err := os.WriteFile(filepath.Join(src, ".git", "config"), []byte("[http]\n\textraheader = Bearer secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(src, "tools", "helper", ".git")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "config"), []byte("[http]\n\textraheader = Bearer nested-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "dirty.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := gitWorkspace(t, src, "rev-parse", "HEAD")

	var buf bytes.Buffer
	if err := WriteWorkspaceExport(src, &buf); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	gotHead, bundle, files, err := ReadWorkspaceExport(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if gotHead != head {
		t.Fatalf("head %s, want %s", gotHead, head)
	}
	if _, ok := files["README"]; !ok {
		t.Fatal("missing committed README")
	}
	if string(files["dirty.txt"].Body) != "uncommitted\n" {
		t.Fatalf("dirty %q", files["dirty.txt"].Body)
	}
	for name := range files {
		if skipExportPath(name) {
			t.Fatalf("exported git dir %q", name)
		}
	}
	if bytes.Contains(raw, []byte("Bearer secret")) || bytes.Contains(raw, []byte("Bearer nested-secret")) {
		t.Fatal("export leaked git credential")
	}
	tr := tar.NewReader(bytes.NewReader(raw))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Uname != "" || hdr.Gname != "" || hdr.Uid != 0 || hdr.Gid != 0 {
			t.Fatalf("host identity in tar header %q", hdr.Name)
		}
		_, _ = io.Copy(io.Discard, tr)
	}

	dest := t.TempDir()
	gitWorkspace(t, dest, "init", "-q", "-b", "main")
	bundlePath := filepath.Join(t.TempDir(), "bundle")
	if err := os.WriteFile(bundlePath, bundle, 0o600); err != nil {
		t.Fatal(err)
	}
	gitWorkspace(t, dest, "fetch", "-q", bundlePath, "+HEAD:refs/heads/from-export")
	if gitWorkspace(t, dest, "rev-parse", "refs/heads/from-export") != head {
		t.Fatal("bundle did not carry HEAD")
	}
}

func TestWriteWorkspaceExportNoGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteWorkspaceExport(dir, &buf); !errors.Is(err, ErrWorkspaceNoGit) {
		t.Fatalf("err %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("wrote %d bytes on unpublished", buf.Len())
	}
}

func initGitWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(dir, "gitconfig"))
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.invalid")
	gitWorkspace(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitWorkspace(t, dir, "add", "README")
	gitWorkspace(t, dir, "commit", "-q", "-m", "initial")
	return dir
}

func gitWorkspace(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

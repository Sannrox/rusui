package env

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestProcessListWorkspaceTopLevelAndPath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	handle := filepath.Join(root, "sess")
	if err := os.MkdirAll(filepath.Join(handle, "dir", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(handle, "note.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(handle, ".hidden"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(handle, "dir", "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(handle, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}

	p := Process{}
	names, err := p.ListWorkspace(handle, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".hidden", "dir", "link", "note.txt"} {
		if !slices.Contains(names, want) {
			t.Fatalf("missing %q in %q", want, names)
		}
	}
	if slices.Contains(names, "nested") || slices.Contains(names, "a.txt") {
		t.Fatalf("recursive %q", names)
	}

	dirNames, err := p.ListWorkspace(handle, "dir")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(dirNames, []string{"a.txt", "nested"}) {
		t.Fatalf("dir %q", dirNames)
	}
}

func TestProcessListWorkspaceRefusesEscapeSymlinkAndMissing(t *testing.T) {
	t.Parallel()
	handle := t.TempDir()
	if err := os.WriteFile(filepath.Join(handle, "note.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(handle, "link")); err != nil {
		t.Fatal(err)
	}
	p := Process{}
	if _, err := p.ListWorkspace(handle, "../secret"); !errors.Is(err, ErrWorkspacePath) {
		t.Fatalf("escape %v", err)
	}
	if _, err := p.ListWorkspace(handle, "/etc"); !errors.Is(err, ErrWorkspacePath) {
		t.Fatalf("abs %v", err)
	}
	if _, err := p.ListWorkspace(handle, "missing"); !errors.Is(err, ErrWorkspaceMissing) {
		t.Fatalf("missing %v", err)
	}
	if _, err := p.ListWorkspace(handle, "note.txt"); !errors.Is(err, ErrWorkspaceNotDir) {
		t.Fatalf("file %v", err)
	}
	if _, err := p.ListWorkspace(handle, "link"); !errors.Is(err, ErrWorkspaceNotDir) {
		t.Fatalf("symlink %v", err)
	}
}

func TestFakeRuntimeWorkspaceList(t *testing.T) {
	t.Parallel()
	f := &FakeRuntime{}
	if err := f.WriteFile("ctr", "note.txt", strings.NewReader("hi"), WorkspaceUploadCap); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteFile("ctr", "dir/a.txt", strings.NewReader("a"), WorkspaceUploadCap); err != nil {
		t.Fatal(err)
	}
	names, err := f.WorkspaceList("ctr", "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"dir", "note.txt"}) {
		t.Fatalf("top %q", names)
	}
	dirNames, err := f.WorkspaceList("ctr", "dir")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(dirNames, []string{"a.txt"}) {
		t.Fatalf("dir %q", dirNames)
	}
	if _, err := f.WorkspaceList("ctr", "../x"); !errors.Is(err, ErrWorkspacePath) {
		t.Fatalf("escape %v", err)
	}
	if _, err := f.WorkspaceList("ctr", "note.txt"); !errors.Is(err, ErrWorkspaceNotDir) {
		t.Fatalf("file %v", err)
	}
	if _, err := f.WorkspaceList("ctr", "absent"); !errors.Is(err, ErrWorkspaceMissing) {
		t.Fatalf("missing %v", err)
	}
	f.Replaced = map[string]bool{"ctr": true}
	if _, err := f.WorkspaceList("ctr", ""); !errors.Is(err, ErrWorkspaceReplaced) {
		t.Fatalf("replaced %v", err)
	}
}

func TestDockerCLIListWorkspaceMapsReplacedRoot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "docker")
	script := "#!/bin/sh\nexit 13\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	d := DockerCLI{Bin: bin}
	if _, err := d.WorkspaceList("box", ""); !errors.Is(err, ErrWorkspaceReplaced) {
		t.Fatalf("replaced %v", err)
	}
}

func TestDockerCLIListWorkspaceParsesNames(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "docker")
	script := "#!/bin/sh\nprintf 'note.txt\\0dir\\0.hidden\\0'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	d := DockerCLI{Bin: bin}
	names, err := d.WorkspaceList("box", "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{".hidden", "dir", "note.txt"}) {
		t.Fatalf("names %q", names)
	}
}

func TestDockerCLIListWorkspaceRejectsPathBeforeExec(t *testing.T) {
	t.Parallel()
	d := DockerCLI{Bin: "/nonexistent/docker"}
	if _, err := d.WorkspaceList("box", "../etc"); !errors.Is(err, ErrWorkspacePath) {
		t.Fatalf("escape %v", err)
	}
}

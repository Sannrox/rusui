package env

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessWriteFileRejectsEscape(t *testing.T) {
	p := Process{Root: t.TempDir()}
	handle := filepath.Join(p.Root, "sess")
	if err := os.MkdirAll(handle, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := p.WriteFile(handle, "../secret", bytes.NewReader([]byte("x")), WorkspaceUploadCap); err == nil {
		t.Fatal("escape")
	}
	if err := p.WriteFile(handle, "/etc/passwd", bytes.NewReader([]byte("x")), WorkspaceUploadCap); err == nil {
		t.Fatal("abs")
	}
	if err := p.WriteFile(handle, "note.txt", bytes.NewReader([]byte("hi")), WorkspaceUploadCap); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(handle, "note.txt"))
	if err != nil || string(b) != "hi" {
		t.Fatalf("got %q %v", b, err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(handle, "link")); err != nil {
		t.Fatal(err)
	}
	if err := p.WriteFile(handle, "link/pwned", bytes.NewReader([]byte("x")), WorkspaceUploadCap); err == nil {
		t.Fatal("intermediate symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned")); err == nil {
		t.Fatal("wrote outside handle")
	}
	if err := p.WriteFile(handle, "sub/dir/ok.txt", bytes.NewReader([]byte("in")), WorkspaceUploadCap); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(handle, "sub", "dir", "ok.txt"))
	if err != nil || string(got) != "in" {
		t.Fatalf("nested %q %v", got, err)
	}
}

func TestProcessWriteFileEnforcesCap(t *testing.T) {
	p := Process{Root: t.TempDir()}
	handle := filepath.Join(p.Root, "sess")
	if err := os.MkdirAll(handle, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := p.WriteFile(handle, "big.bin", strings.NewReader(strings.Repeat("a", 8)), 4); err == nil {
		t.Fatal("cap")
	}
}

func TestContainerWriteFileSourceHasNoReadAll(t *testing.T) {
	b, err := os.ReadFile("workspace_write.go")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("io.ReadAll")) {
		t.Fatal("container put must stream; io.ReadAll buffers the whole upload")
	}
}

func TestDockerCLIWriteFileStreamsViaExec(t *testing.T) {
	bin, logPath := stubDocker(t)
	d := DockerCLI{Bin: bin}
	src := &countReader{r: strings.NewReader(strings.Repeat("x", 4096))}
	if err := d.WriteFile("0123456789abcdef", "note.txt", src, WorkspaceUploadCap); err != nil {
		t.Fatal(err)
	}
	if src.n != 4096 {
		t.Fatalf("read %d", src.n)
	}
	logb, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logb)
	if !strings.Contains(log, "exec -i 0123456789abcdef tee /workspace/note.txt") {
		t.Fatalf("expected stdin tee, log:\n%s", log)
	}
	if strings.Contains(log, " cp ") || strings.Contains(log, "\ncp ") {
		t.Fatalf("must not docker cp a host temp, log:\n%s", log)
	}
}

func TestDockerCLIWriteFileEnforcesCapWithoutFullBuffer(t *testing.T) {
	bin, _ := stubDocker(t)
	d := DockerCLI{Bin: bin}
	src := &countReader{r: strings.NewReader(strings.Repeat("a", 8))}
	if err := d.WriteFile("0123456789abcdef", "big.bin", src, 4); err == nil {
		t.Fatal("cap")
	}
	if src.n != 5 {
		t.Fatalf("must stop at max+1, read %d", src.n)
	}
}

func TestProcessReadFileReturnsBytesAndNested(t *testing.T) {
	t.Parallel()
	p := Process{Root: t.TempDir()}
	handle := filepath.Join(p.Root, "sess")
	if err := os.MkdirAll(filepath.Join(handle, "sub", "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	bin := []byte{0x00, 0x01, 0xff, 'a'}
	if err := os.WriteFile(filepath.Join(handle, "note.bin"), bin, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(handle, "sub", "dir", "ok.txt"), []byte("in"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := p.ReadFile(handle, "note.bin", WorkspaceUploadCap)
	if err != nil || !bytes.Equal(got, bin) {
		t.Fatalf("binary %q %v", got, err)
	}
	nested, err := p.ReadFile(handle, "sub/dir/ok.txt", WorkspaceUploadCap)
	if err != nil || string(nested) != "in" {
		t.Fatalf("nested %q %v", nested, err)
	}
}

func TestProcessReadFileRejectsEscapeSymlinkDirAndCap(t *testing.T) {
	t.Parallel()
	p := Process{Root: t.TempDir()}
	handle := filepath.Join(p.Root, "sess")
	if err := os.MkdirAll(filepath.Join(handle, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(handle, "note.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ReadFile(handle, "../secret", WorkspaceUploadCap); !errors.Is(err, ErrWorkspacePath) {
		t.Fatalf("escape %v", err)
	}
	if _, err := p.ReadFile(handle, "/etc/passwd", WorkspaceUploadCap); !errors.Is(err, ErrWorkspacePath) {
		t.Fatalf("abs %v", err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(handle, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ReadFile(handle, "link/secret", WorkspaceUploadCap); !errors.Is(err, ErrWorkspacePath) {
		t.Fatalf("symlink %v", err)
	}
	if _, err := p.ReadFile(handle, "dir", WorkspaceUploadCap); !errors.Is(err, ErrWorkspaceNotFile) {
		t.Fatalf("dir %v", err)
	}
	if _, err := p.ReadFile(handle, "absent", WorkspaceUploadCap); !errors.Is(err, ErrWorkspaceMissing) {
		t.Fatalf("missing %v", err)
	}
	if err := os.WriteFile(filepath.Join(handle, "big.bin"), []byte("abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ReadFile(handle, "big.bin", 4); !errors.Is(err, ErrWorkspaceOversized) {
		t.Fatalf("cap %v", err)
	}
}

func TestDockerCLIReadWorkspaceMapsExitAndRejectsPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf 'hi\\000'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := DockerCLI{Bin: bin}
	got, err := d.ReadWorkspace("box", "note.bin", WorkspaceUploadCap)
	if err != nil || !bytes.Equal(got, []byte("hi\x00")) {
		t.Fatalf("body %q %v", got, err)
	}
	if _, err := d.ReadWorkspace("box", "../etc", WorkspaceUploadCap); !errors.Is(err, ErrWorkspacePath) {
		t.Fatalf("escape %v", err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 13\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ReadWorkspace("box", "note.bin", WorkspaceUploadCap); !errors.Is(err, ErrWorkspaceReplaced) {
		t.Fatalf("replaced %v", err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 14\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ReadWorkspace("box", "note.bin", WorkspaceUploadCap); !errors.Is(err, ErrWorkspacePath) {
		t.Fatalf("symlink %v", err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 12\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ReadWorkspace("box", "note.bin", 4); !errors.Is(err, ErrWorkspaceOversized) {
		t.Fatalf("oversize %v", err)
	}
}

func TestContainerReadFileUsesRuntime(t *testing.T) {
	rt := &FakeRuntime{}
	if err := rt.WriteFile("ctr-1", "shot.png", bytes.NewReader([]byte("png")), WorkspaceUploadCap); err != nil {
		t.Fatal(err)
	}
	c := Container{RT: rt, Image: "rusui-guest:test"}
	got, err := c.ReadFile("ctr-1", "shot.png", WorkspaceUploadCap)
	if err != nil || string(got) != "png" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := c.ReadFile("ctr-1", "../x", WorkspaceUploadCap); !errors.Is(err, ErrWorkspacePath) {
		t.Fatalf("escape %v", err)
	}
}

func TestContainerWriteFileUsesRuntime(t *testing.T) {
	rt := &FakeRuntime{}
	c := Container{RT: rt, Image: "rusui-guest:test"}
	if err := c.WriteFile("ctr-1", "shot.png", bytes.NewReader([]byte("png")), WorkspaceUploadCap); err != nil {
		t.Fatal(err)
	}
	if string(rt.Contents["ctr-1"]["shot.png"]) != "png" {
		t.Fatalf("%q", rt.Contents["ctr-1"]["shot.png"])
	}
}

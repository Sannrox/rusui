package env

import (
	"bytes"
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

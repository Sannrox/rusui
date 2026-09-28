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

package env

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessPortForwardStaysInWorkspace(t *testing.T) {
	root := t.TempDir()
	p := Process{Root: root}
	handle := filepath.Join(root, "sess")
	if err := os.MkdirAll(handle, 0o700); err != nil {
		t.Fatal(err)
	}
	addr, err := p.PortForward(handle, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if addr != net.JoinHostPort("127.0.0.1", "3000") {
		t.Fatalf("addr %q", addr)
	}
	if _, err := p.PortForward("", 3000); err == nil {
		t.Fatal("empty handle")
	}
	if _, err := p.PortForward(handle, 80); err == nil {
		t.Fatal("privileged port")
	}
}

func TestContainerPortForwardUsesGuestAddr(t *testing.T) {
	rt := &FakeRuntime{GuestAddrs: map[string]string{"ctr-1/3000": "10.88.0.12:3000"}}
	c := Container{RT: rt, Image: "rusui-guest:test"}
	addr, err := c.PortForward("ctr-1", 3000)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "10.88.0.12:3000" {
		t.Fatalf("addr %q", addr)
	}
	if _, err := c.PortForward("ctr-1", 4000); err == nil {
		t.Fatal("missing mapping")
	}
}

func TestDockerCLIGuestAddrReusesSidecar(t *testing.T) {
	bin, logPath := stubDocker(t)
	d := DockerCLI{Bin: bin}
	addr, err := d.GuestAddr("0123456789abcdef", 3000)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "127.0.0.1:54321" {
		t.Fatalf("addr %q", addr)
	}
	again, err := d.GuestAddr("0123456789abcdef", 3000)
	if err != nil || again != addr {
		t.Fatalf("reuse %q %q %v", again, addr, err)
	}
	logb, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	runs := 0
	for line := range strings.SplitSeq(string(logb), "\n") {
		if strings.HasPrefix(line, "run ") {
			runs++
		}
	}
	if runs != 1 {
		t.Fatalf("sidecar runs %d\n%s", runs, logb)
	}
	if err := d.Stop("0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GuestAddr("0123456789abcdef", 3000); err != nil {
		t.Fatal(err)
	}
	logb, _ = os.ReadFile(logPath)
	runs = 0
	rms := 0
	for line := range strings.SplitSeq(string(logb), "\n") {
		if strings.HasPrefix(line, "run ") {
			runs++
		}
		if strings.HasPrefix(line, "rm -f rusui-fwd-") {
			rms++
		}
	}
	if runs != 2 || rms < 1 {
		t.Fatalf("after stop runs=%d rms=%d\n%s", runs, rms, logb)
	}
}

package env

import (
	"net"
	"os"
	"path/filepath"
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

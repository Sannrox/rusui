//go:build darwin || linux

package sumika

import (
	"fmt"
	"net"
	"os"
	"syscall"
)

func sameUserSocket(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("sumika socket %q: %w", path, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("sumika path %q is not a Unix socket", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("sumika socket %q is not owned by the current user", path)
	}
	return nil
}

func sameUserPeer(conn *net.UnixConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return fmt.Errorf("sumika socket peer credentials: %w", err)
	}
	var uid uint32
	var peerErr error
	if err := raw.Control(func(fd uintptr) {
		uid, peerErr = unixPeerUID(int(fd))
	}); err != nil {
		return fmt.Errorf("sumika socket peer credentials: %w", err)
	}
	if peerErr != nil {
		return fmt.Errorf("sumika socket peer credentials: %w", peerErr)
	}
	if uid != uint32(os.Geteuid()) {
		return fmt.Errorf("sumika socket peer is not owned by the current user")
	}
	return nil
}

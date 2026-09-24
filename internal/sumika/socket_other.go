//go:build !darwin && !linux

package sumika

import (
	"fmt"
	"net"
)

func sameUserSocket(path string) error {
	return fmt.Errorf("sumika local runtime is unsupported on this platform: %s", path)
}

func sameUserPeer(*net.UnixConn) error {
	return fmt.Errorf("sumika local runtime is unsupported on this platform")
}

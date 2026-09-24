//go:build darwin || linux

package sumika

import (
	"fmt"
	"os"
	"syscall"
)

// LockAttach serializes Rusui attach handshakes across CLI processes.
func LockAttach() (func(), error) {
	socketPath, err := DefaultSocketPath()
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(socketPath+".rusui-attach.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("sumika attach lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("sumika attach lock: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

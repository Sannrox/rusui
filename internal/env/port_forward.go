package env

import (
	"fmt"
	"net"
	"strconv"
)

// PortForwarder maps a guest listen port to a dial address on the plane host.
type PortForwarder interface {
	PortForward(handle string, port int) (string, error)
}

func validGuestPort(port int) error {
	if port < 1024 || port > 65535 {
		return fmt.Errorf("env: port %d out of range", port)
	}
	return nil
}

func (p Process) PortForward(handle string, port int) (string, error) {
	if err := validGuestPort(port); err != nil {
		return "", err
	}
	if handle == "" {
		return "", fmt.Errorf("env: process handle required")
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), nil
}

type guestAddrRuntime interface {
	GuestAddr(id string, port int) (string, error)
}

func (c Container) PortForward(handle string, port int) (string, error) {
	if err := validGuestPort(port); err != nil {
		return "", err
	}
	if handle == "" {
		return "", fmt.Errorf("env: empty handle")
	}
	g, ok := c.RT.(guestAddrRuntime)
	if !ok {
		return "", fmt.Errorf("env: runtime cannot forward ports")
	}
	return g.GuestAddr(handle, port)
}

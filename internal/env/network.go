package env

import (
	"fmt"
	"net"
	"strings"
)

const (
	PlaneHost      = "rusui.plane"
	TrustedNetwork = "rusui-trusted"
	GuestCAPath    = "/usr/local/share/ca-certificates/rusui-plane.crt"
)

// ApplyTrustedNetwork is the dogfood egress class: the guest may reach only
// the plane proxy hostname over the trusted network. IPv6 is off until an
// operator enables it with a tested topology.
func ApplyTrustedNetwork(spec *Spec) {
	if spec == nil {
		return
	}
	spec.Network = TrustedNetwork
	spec.ExtraHosts = []string{PlaneHost + ":host-gateway"}
	spec.DisableIPv6 = true
}

// TrustedRunArgs are the Docker/Podman flags that implement ApplyTrustedNetwork.
func TrustedRunArgs(spec Spec) []string {
	args := []string{"--network", spec.Network}
	for _, h := range spec.ExtraHosts {
		args = append(args, "--add-host", h)
	}
	if spec.DisableIPv6 {
		args = append(args, "--sysctl", "net.ipv6.conf.all.disable_ipv6=1")
	}
	if spec.CAFile != "" {
		args = append(args, "-v", spec.CAFile+":"+GuestCAPath+":ro")
	}
	return args
}

// GuestDialAllowed is the same policy the container flags encode. Direct
// addresses, IPv6, host loopback/services, and unapproved proxy names fail.
func GuestDialAllowed(host string, ipv6 bool) error {
	if ipv6 {
		return fmt.Errorf("env: ipv6 egress denied")
	}
	h := strings.TrimSpace(strings.ToLower(host))
	if h == "" {
		return fmt.Errorf("env: empty dial host")
	}
	if h == PlaneHost {
		return nil
	}
	if ip := net.ParseIP(h); ip != nil {
		return fmt.Errorf("env: direct address %s denied", host)
	}
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return fmt.Errorf("env: host service %s denied", host)
	}
	return fmt.Errorf("env: unapproved egress target %s", host)
}

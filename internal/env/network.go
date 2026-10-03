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
	// NoNetwork is the container network of a managed guest (ADR 0047).
	NoNetwork = "none"
	// Guest loopback addresses the guest link listens on. A name maps to
	// one of them in the guest's hosts file; only a link target makes the
	// address answer.
	GuestPlaneIP     = "127.0.0.2"
	GuestGitHubIP    = "127.0.0.3"
	GuestGitHubAPIIP = "127.0.0.4"
)

// ApplyTrustedNetwork is the trusted egress class: the guest has no
// network, and the guest link is the only way out (ADR 0047). The plane
// name always resolves; the GitHub names resolve so an implement turn
// whose link forwards them can use them, and refuse otherwise. IPv6 is
// off until an operator enables it with a tested topology.
func ApplyTrustedNetwork(spec *Spec) {
	if spec == nil {
		return
	}
	spec.Network = NoNetwork
	spec.ExtraHosts = []string{
		PlaneHost + ":" + GuestPlaneIP,
		"github.com:" + GuestGitHubIP,
		"api.github.com:" + GuestGitHubAPIIP,
	}
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

// GuestDialAllowed is the trusted class the guest link encodes for a
// non-implement turn. Direct addresses, IPv6, host loopback/services, and
// unapproved proxy names fail.
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

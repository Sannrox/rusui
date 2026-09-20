package env

import (
	"strings"
	"testing"
)

func TestGuestDialAllowedTrustedPlaneOnly(t *testing.T) {
	if err := GuestDialAllowed(PlaneHost, false); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		host string
		ipv6 bool
	}{
		{"8.8.8.8", false},
		{"1.2.3.4", false},
		{PlaneHost, true},
		{"::1", true},
		{"127.0.0.1", false},
		{"localhost", false},
		{"api.x.ai", false},
		{"github.com", false},
		{"api.github.com", false},
		{"evil.proxy.example", false},
	}
	for _, tc := range cases {
		if err := GuestDialAllowed(tc.host, tc.ipv6); err == nil {
			t.Fatalf("%s ipv6=%v allowed", tc.host, tc.ipv6)
		}
	}
}

func TestTrustedRunArgsEncodePolicy(t *testing.T) {
	spec := Spec{Image: "rusui-guest:test", CAFile: "/etc/rusui/plane.crt"}
	ApplyTrustedNetwork(&spec)
	args := strings.Join(TrustedRunArgs(spec), " ")
	for _, want := range []string{
		"--network " + TrustedNetwork,
		"--add-host " + PlaneHost + ":host-gateway",
		"--sysctl net.ipv6.conf.all.disable_ipv6=1",
		"-v /etc/rusui/plane.crt:" + GuestCAPath + ":ro",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("missing %q in %s", want, args)
		}
	}
	if err := GuestDialAllowed(PlaneHost, spec.DisableIPv6); err == nil {
		t.Fatal("ipv6 still allowed after disable flag")
	}
}

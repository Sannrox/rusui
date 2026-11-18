package env

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

// The full class grants public destinations only: every address that
// reaches this host, a private network, or cloud metadata is refused,
// including the IPv4 forms hidden inside IPv6 (#751).
func TestEgressAddrAllowed(t *testing.T) {
	host := netip.MustParseAddr("203.0.113.7")
	for addr, want := range map[string]bool{
		"93.184.215.14":      true,
		"2606:4700::1111":    true,
		"127.0.0.1":          false,
		"::1":                false,
		"10.1.2.3":           false,
		"172.17.0.1":         false,
		"192.168.1.1":        false,
		"169.254.169.254":    false,
		"fe80::1":            false,
		"fd00::1":            false,
		"0.0.0.0":            false,
		"224.0.0.1":          false,
		"255.255.255.255":    false,
		"100.64.0.1":         false,
		"::ffff:10.0.0.1":    false,
		"2002:a00:1::":       false,
		"64:ff9b::a00:1":     false,
		"2001:0:a00:1::":     false,
		host.String():        false,
		"::ffff:203.0.113.7": false,
	} {
		if got := egressAddrAllowed(netip.MustParseAddr(addr), []netip.Addr{host}); got != want {
			t.Errorf("%s: got %v want %v", addr, got, want)
		}
	}
}

// The proxy refuses a host service whether the guest names it by address
// or by a name that resolves to it, over CONNECT and plain HTTP alike, and
// a tunnel to any port but 443, so SSH has no route.
func TestEgressProxyRefusesHostServicesAndNonHTTPSTunnels(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("host")) }))
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	p, err := StartEgressProxy()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	for _, req := range []string{
		// Port 443 passes the port check, so only the address guard refuses.
		"CONNECT 127.0.0.1:443 HTTP/1.1\r\nHost: 127.0.0.1:443\r\n\r\n",
		"CONNECT localhost:443 HTTP/1.1\r\nHost: localhost:443\r\n\r\n",
		"GET http://127.0.0.1:" + port + "/ HTTP/1.1\r\nHost: 127.0.0.1:" + port + "\r\n\r\n",
		"GET http://localhost:" + port + "/ HTTP/1.1\r\nHost: localhost:" + port + "\r\n\r\n",
		"CONNECT example.com:22 HTTP/1.1\r\nHost: example.com:22\r\n\r\n",
	} {
		c, err := net.Dial("tcp", p.Addr())
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Fprint(c, req)
		resp, err := http.ReadResponse(bufio.NewReader(c), nil)
		_ = c.Close()
		if err != nil {
			t.Fatalf("%q: %v", req, err)
		}
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%q: status %d", strings.SplitN(req, "\r", 2)[0], resp.StatusCode)
		}
	}
}

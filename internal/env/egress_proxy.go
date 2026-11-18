package env

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"sync"
	"syscall"
	"time"
)

// Egress class full (ADR 0093): the guest keeps no network, and its
// HTTP(S)_PROXY points at a guest link target that the runner serves with
// an HTTP and CONNECT forward proxy. The proxy resolves each name itself
// and refuses, at dial time, any address that is not a public unicast
// address or that belongs to this host. A guest process that ignores the
// proxy settings has no route at all.
const (
	GuestProxyIP   = "127.0.0.5"
	GuestProxyPort = 3128
)

// GuestProxyEnv is the guest environment that routes HTTP(S) through the
// egress proxy. The plane and guest-local names stay direct.
func GuestProxyEnv() []string {
	u := fmt.Sprintf("http://%s:%d", GuestProxyIP, GuestProxyPort)
	none := PlaneHost + ",localhost,127.0.0.1"
	return []string{
		"HTTPS_PROXY=" + u, "https_proxy=" + u,
		"HTTP_PROXY=" + u, "http_proxy=" + u,
		"NO_PROXY=" + none, "no_proxy=" + none,
	}
}

var errEgressDenied = errors.New("egress destination denied")

// Address ranges IsGlobalUnicast admits that still reach a private network
// or carry an embedded IPv4 address the check would not see.
var egressDenied = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),     // this network
	netip.MustParsePrefix("240.0.0.0/4"),   // reserved
	netip.MustParsePrefix("100.64.0.0/10"), // shared address space (CGNAT)
	netip.MustParsePrefix("192.0.0.0/24"),  // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("2002::/16"),     // 6to4
	netip.MustParsePrefix("64:ff9b::/96"),  // NAT64
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001::/32"), // Teredo
}

// egressAddrAllowed reports whether the proxy may connect to a. Loopback,
// private, link-local (cloud metadata included), multicast, unspecified,
// the ranges above, and every address of this host are refused.
func egressAddrAllowed(a netip.Addr, local []netip.Addr) bool {
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() {
		return false
	}
	for _, p := range egressDenied {
		if p.Contains(a) {
			return false
		}
	}
	for _, l := range local {
		if l.Unmap() == a {
			return false
		}
	}
	return true
}

func localAddrs() []netip.Addr {
	addrs, _ := net.InterfaceAddrs()
	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil {
			out = append(out, p.Addr())
		}
	}
	return out
}

// egressDialer checks the resolved address of every connection attempt,
// so a name that resolves (or rebinds) to a refused address fails.
var egressDialer = &net.Dialer{
	Timeout: linkDialLimit,
	Control: func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil || !egressAddrAllowed(ap.Addr(), localAddrs()) {
			return fmt.Errorf("%w: %s", errEgressDenied, address)
		}
		return nil
	},
}

// EgressProxy is one turn's forward proxy on host loopback.
type EgressProxy struct {
	ln  net.Listener
	srv *http.Server
	tr  *http.Transport
	ctx context.Context
	// mu orders a tunnel's wg.Add before Close's wg.Wait: a tunnel that
	// starts after cancel closes at once instead.
	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// StartEgressProxy listens on a host loopback port. Addr is the guest link
// target's Dial address; Close stops the proxy and its open tunnels.
func StartEgressProxy() (*EgressProxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("env: egress proxy: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &EgressProxy{ln: ln, ctx: ctx, cancel: cancel, tr: &http.Transport{
		DialContext:           egressDialer.DialContext,
		ResponseHeaderTimeout: time.Minute,
		IdleConnTimeout:       30 * time.Second,
	}}
	fwd := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL = r.In.URL
			r.Out.Host = r.In.Host
		},
		Transport: p.tr,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, egressStatusText(err), egressStatus(err))
		},
	}
	p.srv = &http.Server{
		ReadHeaderTimeout: linkDialLimit,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodConnect:
				p.tunnel(w, r)
			case r.URL.IsAbs() && r.URL.Scheme == "http":
				fwd.ServeHTTP(w, r)
			default:
				http.Error(w, "proxy expects CONNECT or an absolute http URL", http.StatusBadRequest)
			}
		}),
	}
	go func() { _ = p.srv.Serve(ln) }()
	return p, nil
}

// Addr is the proxy's host address.
func (p *EgressProxy) Addr() string { return p.ln.Addr().String() }

// Close stops accepting, ends every tunnel, and waits for them.
func (p *EgressProxy) Close() {
	p.mu.Lock()
	p.cancel()
	p.mu.Unlock()
	_ = p.srv.Close()
	p.tr.CloseIdleConnections()
	p.wg.Wait()
}

func egressStatus(err error) int {
	if errors.Is(err, errEgressDenied) {
		return http.StatusForbidden
	}
	return http.StatusBadGateway
}

func egressStatusText(err error) string {
	if errors.Is(err, errEgressDenied) {
		return "egress destination denied"
	}
	return "egress destination unreachable"
}

// tunnel serves CONNECT host:443: dial through the guarded dialer, then
// splice both directions with half close. Other ports are refused, so a
// tunnel carries HTTPS, not SSH or mail.
func (p *EgressProxy) tunnel(w http.ResponseWriter, r *http.Request) {
	if _, port, err := net.SplitHostPort(r.Host); err != nil || port != "443" {
		http.Error(w, "egress destination denied", http.StatusForbidden)
		return
	}
	up, err := egressDialer.DialContext(r.Context(), "tcp", r.Host)
	if err != nil {
		http.Error(w, egressStatusText(err), egressStatus(err))
		return
	}
	h, ok := w.(http.Hijacker)
	if !ok {
		_ = up.Close()
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	down, rw, err := h.Hijack()
	if err != nil {
		_ = up.Close()
		return
	}
	p.mu.Lock()
	if p.ctx.Err() != nil {
		p.mu.Unlock()
		_ = up.Close()
		_ = down.Close()
		return
	}
	p.wg.Add(1)
	p.mu.Unlock()
	go func() {
		defer p.wg.Done()
		defer func() { _ = up.Close(); _ = down.Close() }()
		if _, err := io.WriteString(down, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
			return
		}
		stop := context.AfterFunc(p.ctx, func() { _ = up.Close(); _ = down.Close() })
		defer stop()
		done := make(chan struct{})
		go func() {
			// rw.Reader holds bytes the client sent after the request.
			_, _ = io.Copy(up, rw.Reader)
			closeWrite(up)
			close(done)
		}()
		_, _ = io.Copy(down, up)
		closeWrite(down)
		<-done
	}()
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
}

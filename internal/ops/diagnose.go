package ops

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/policy"
)

// Topology is the one unattended profile D1/D4 proved. Other hosts are not claimed.
const Topology = "linux-or-macos/one-runner/docker-or-podman-cli/rusui-guest/grok-acp/loopback"

const (
	StatusReady         = "ready"
	StatusMisconfigured = "misconfigured"
	StatusUnavailable   = "unavailable"
)

type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Detail  string `json:"detail"`
	Blocker bool   `json:"blocker"`
}

type Report struct {
	Topology string  `json:"topology"`
	Ready    bool    `json:"ready"`
	Checks   []Check `json:"checks"`
}

type Options struct {
	PolicyPath  string
	Addr        string
	PlaneURL    string
	CAFile      string
	LookRuntime func() (env.Runtime, error)
	Env         func(string) string
	HTTP        *http.Client
}

func Diagnose(opt Options) Report {
	getenv := opt.Env
	if getenv == nil {
		getenv = os.Getenv
	}
	look := opt.LookRuntime
	if look == nil {
		look = env.LookRuntime
	}
	r := Report{Topology: Topology, Ready: true}
	r.add(checkPolicy(opt.PolicyPath))
	r.add(checkLoopback(opt.Addr, getenv))
	rt, rtErr := look()
	r.add(checkRuntime(rt, rtErr))
	r.add(checkGuestImage(getenv, rtErr == nil))
	r.add(checkPlaneTLS(getenv, rtErr == nil))
	r.add(checkPlaneCA(getenv, rtErr == nil))
	r.add(checkPlane(opt.PlaneURL, opt.HTTP, opt.CAFile))
	for _, c := range r.Checks {
		if c.Blocker && c.Status != StatusReady {
			r.Ready = false
			break
		}
	}
	return r
}

func (r *Report) add(c Check) {
	r.Checks = append(r.Checks, c)
}

func checkPolicy(path string) Check {
	c := Check{Name: "policy", Blocker: true}
	if path == "" {
		c.Status = StatusMisconfigured
		c.Detail = "policy path unset"
		return c
	}
	if _, err := os.Stat(path); err != nil {
		c.Status = StatusMisconfigured
		c.Detail = "policy file missing"
		return c
	}
	if _, err := policy.Load(path); err != nil {
		c.Status = StatusMisconfigured
		c.Detail = "policy failed closed"
		return c
	}
	c.Status = StatusReady
	c.Detail = "policy v2 loaded"
	return c
}

func checkLoopback(addr string, getenv func(string) string) Check {
	c := Check{Name: "loopback", Blocker: true}
	if addr == "" {
		c.Status = StatusReady
		c.Detail = "addr not supplied"
		c.Blocker = false
		return c
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	loop := host == "localhost" || (ip != nil && ip.IsLoopback())
	if loop {
		c.Status = StatusReady
		c.Detail = "listen is loopback"
		return c
	}
	if getenv("RUSUI_WORKER_SECRET") == "" || getenv("RUSUI_WEBHOOK_SECRET") == "" {
		c.Status = StatusMisconfigured
		c.Detail = "non-loopback listen requires secrets"
		return c
	}
	c.Status = StatusReady
	c.Detail = "non-loopback listen with secrets set"
	return c
}

func checkRuntime(_ env.Runtime, err error) Check {
	c := Check{Name: "runtime", Blocker: true}
	if err != nil {
		c.Status = StatusUnavailable
		c.Detail = "docker or podman not found"
		return c
	}
	c.Status = StatusReady
	c.Detail = "container CLI present"
	return c
}

func checkGuestImage(getenv func(string) string, runtimeOK bool) Check {
	c := Check{Name: "guest_image", Blocker: runtimeOK}
	if getenv("RUSUI_GUEST_IMAGE") == "" {
		c.Status = StatusMisconfigured
		c.Detail = "RUSUI_GUEST_IMAGE unset"
		if !runtimeOK {
			c.Blocker = false
		}
		return c
	}
	c.Status = StatusReady
	c.Detail = "guest image name set"
	return c
}

func checkPlaneTLS(getenv func(string) string, runtimeOK bool) Check {
	c := Check{Name: "plane_tls", Blocker: runtimeOK}
	cert, key := getenv("RUSUI_TLS_CERT"), getenv("RUSUI_TLS_KEY")
	if cert == "" || key == "" {
		c.Status = StatusMisconfigured
		c.Detail = "RUSUI_TLS_CERT and RUSUI_TLS_KEY required for container guests"
		if !runtimeOK {
			c.Blocker = false
		}
		return c
	}
	if _, err := os.Stat(cert); err != nil {
		c.Status = StatusMisconfigured
		c.Detail = "TLS cert file missing"
		return c
	}
	if _, err := os.Stat(key); err != nil {
		c.Status = StatusMisconfigured
		c.Detail = "TLS key file missing"
		return c
	}
	c.Status = StatusReady
	c.Detail = "TLS cert and key files present"
	return c
}

func checkPlaneCA(getenv func(string) string, runtimeOK bool) Check {
	c := Check{Name: "plane_ca", Blocker: runtimeOK}
	ca := getenv("RUSUI_PLANE_CA")
	if ca == "" {
		c.Status = StatusMisconfigured
		c.Detail = "RUSUI_PLANE_CA unset"
		if !runtimeOK {
			c.Blocker = false
		}
		return c
	}
	if _, err := os.Stat(ca); err != nil {
		c.Status = StatusMisconfigured
		c.Detail = "plane CA file missing"
		return c
	}
	c.Status = StatusReady
	c.Detail = "plane CA file present at " + env.GuestCAPath
	return c
}

func TLSClient(caFile string) (*http.Client, error) {
	if caFile == "" {
		return &http.Client{Timeout: 3 * time.Second}, nil
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("env: invalid plane CA")
	}
	return &http.Client{
		Timeout:   3 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
	}, nil
}

func checkPlane(url string, client *http.Client, caFile string) Check {
	c := Check{Name: "plane", Blocker: url != ""}
	if url == "" {
		c.Status = StatusReady
		c.Detail = "plane URL not supplied"
		c.Blocker = false
		return c
	}
	if client == nil {
		var err error
		client, err = TLSClient(caFile)
		if err != nil {
			c.Status = StatusMisconfigured
			c.Detail = "plane CA unreadable"
			return c
		}
	}
	res, err := client.Get(strings.TrimRight(url, "/") + "/healthz")
	if err != nil {
		c.Status = StatusUnavailable
		c.Detail = "plane unreachable"
		return c
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64))
	if res.StatusCode != http.StatusOK || strings.TrimSpace(string(b)) != "ok" {
		c.Status = StatusUnavailable
		c.Detail = fmt.Sprintf("plane healthz status %d", res.StatusCode)
		return c
	}
	c.Status = StatusReady
	c.Detail = "plane /healthz ok"
	return c
}

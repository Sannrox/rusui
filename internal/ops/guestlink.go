package ops

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/sannrox/rusui/internal/env"
)

// guestProbeJS runs in a throwaway guest: it fetches the plane health
// endpoint through the guest link and tries one direct address, which a
// network-less guest must not reach.
const guestProbeJS = `const u=process.argv[1];const t=(x)=>fetch(x,{signal:AbortSignal.timeout(5000)}).then(r=>r.text()).catch(()=>null);
(async()=>{const p=await t(u);const d=await t("https://1.1.1.1/");console.log("plane="+(p&&p.trim()==="ok"?"ok":"fail")+" direct="+(d===null?"blocked":"open"));})()`

type outputRuntime interface {
	ExecOutput(id string, cmd []string) ([]byte, bool, error)
}

// CheckGuestLink starts a throwaway guest from the configured image with
// no network, opens a guest link to the plane, and checks that the plane
// answers through it and that a direct address does not (ADR 0047). It
// starts a container, so only `rusui diagnose` runs it, not /readyz.
func CheckGuestLink(rt env.Runtime, getenv func(string) string, planeURL string) Check {
	c := Check{Name: "guest_link", Blocker: true}
	image := getenv("RUSUI_GUEST_IMAGE")
	if rt == nil || image == "" || planeURL == "" {
		c.Status = StatusReady
		c.Blocker = false
		c.Detail = "container topology not configured"
		return c
	}
	u, err := url.Parse(planeURL)
	if err != nil || u.Port() == "" {
		c.Status = StatusMisconfigured
		c.Detail = "plane URL needs an explicit port"
		return c
	}
	port, _ := strconv.Atoi(u.Port())
	out, ok := rt.(outputRuntime)
	if !ok {
		c.Status = StatusUnavailable
		c.Detail = "runtime cannot exec in a guest"
		return c
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	ctr := env.Container{RT: rt, Image: image, CAFile: getenv("RUSUI_PLANE_CA")}
	handle, err := ctr.Create("diagnose-link-" + hex.EncodeToString(b[:]))
	if err != nil {
		c.Status = StatusUnavailable
		c.Detail = "cannot start a guest from RUSUI_GUEST_IMAGE"
		return c
	}
	defer func() { _ = ctr.Destroy(handle) }()
	link, err := env.StartLink(ctr, handle, env.PlaneLinkTargets(net.JoinHostPort(u.Hostname(), u.Port()), port, false))
	if err != nil {
		c.Status = StatusUnavailable
		c.Detail = "guest link did not start"
		return c
	}
	defer link.Close()
	health := (&url.URL{Scheme: u.Scheme, Host: net.JoinHostPort(env.PlaneHost, u.Port()), Path: "/healthz"}).String()
	res, _, err := out.ExecOutput(handle, []string{"env", "NODE_EXTRA_CA_CERTS=" + env.GuestCAPath, "node", "-e", guestProbeJS, health})
	// The probe's verdict is its last line; Node may print warnings
	// before it on the same captured output.
	got := strings.TrimSpace(string(res))
	if i := strings.LastIndexByte(got, '\n'); i >= 0 {
		got = strings.TrimSpace(got[i+1:])
	}
	switch {
	case err != nil:
		c.Status = StatusUnavailable
		c.Detail = "guest probe failed"
	case got == "plane=ok direct=blocked":
		c.Status = StatusReady
		c.Detail = "guest reached the plane through the link; direct egress blocked"
	case strings.Contains(got, "direct=open"):
		c.Status = StatusMisconfigured
		c.Detail = "guest reached a direct address: egress is not confined"
	default:
		c.Status = StatusUnavailable
		c.Detail = fmt.Sprintf("guest could not reach the plane through the link (%s)", got)
	}
	return c
}

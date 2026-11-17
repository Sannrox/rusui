package env

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// egressProbeImage is the reference guest image's Node base (see
// build/guest-image/Dockerfile): the guest link needs Node in the guest.
const egressProbeImage = "node:22-bookworm-slim@sha256:48e4b67d85f87bd551df43704e24d252f56cc5f8e9718841aace50f19948f0f9"

// egressProbeJS tries each argv target ("label=host:port") directly and
// prints "label=open" or "label=blocked", then fetches the plane health
// URL through the guest link and prints "plane=ok" or "plane=fail".
const egressProbeJS = `const net=require("net");
const probe=(label,host,port)=>new Promise(res=>{const s=net.connect({host,port:+port});
const done=v=>{s.destroy();res(label+"="+v);};s.setTimeout(3000,()=>done("blocked"));
s.on("connect",()=>done("open"));s.on("error",()=>done("blocked"));});
(async()=>{const [plane,...targets]=process.argv.slice(1);const out=[];
for(const t of targets){const [label,addr]=t.split("=");const i=addr.lastIndexOf(":");
out.push(await probe(label,addr.slice(0,i).replace(/^\[|\]$/g,""),addr.slice(i+1)));}
const p=await fetch(plane,{signal:AbortSignal.timeout(5000)}).then(r=>r.text()).catch(()=>null);
out.push("plane="+(p&&p.trim()==="ok"?"ok":"fail"));console.log(out.join(" "));})()`

// A trusted-class guest started by the container driver reaches the plane
// through its guest link and nothing else (#683). In CI a missing runtime
// fails the test instead of skipping it, so the proof cannot go quiet.
func TestLiveTrustedGuestReachesOnlyLinkTargets(t *testing.T) {
	unavailable := t.Skipf
	if os.Getenv("CI") != "" {
		unavailable = t.Fatalf
	}
	rt, err := LookRuntime()
	if err != nil {
		unavailable("container runtime required for live egress proof: %v", err)
		return
	}
	plane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(plane.Close)
	_, portText, _ := net.SplitHostPort(plane.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)

	d := Container{RT: rt, Image: egressProbeImage}
	if err := exec.Command(rt.(DockerCLI).bin(), "pull", "-q", egressProbeImage).Run(); err != nil {
		unavailable("cannot pull %s: %v", egressProbeImage, err)
		return
	}
	id, err := d.Create(fmt.Sprintf("egress-%d", time.Now().UnixNano()%1_000_000_000))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "cannot connect") {
			unavailable("container runtime unavailable: %v", err)
			return
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Destroy(id) })
	link, err := StartLink(d, id, PlaneLinkTargets(plane.Listener.Addr().String(), port, false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(link.Close)

	// A listener on the runtime's bridge gateway, so a guest that could
	// reach the host through the gateway would connect, not be refused.
	gwOut, err := exec.Command(rt.(DockerCLI).bin(), "network", "inspect", "bridge",
		"--format", "{{range .IPAM.Config}}{{.Gateway}} {{end}}").Output()
	if err != nil || len(strings.Fields(string(gwOut))) == 0 {
		t.Fatalf("bridge gateway: %v %q", err, gwOut)
	}
	gw := strings.Fields(string(gwOut))[0]
	gwLn, err := net.Listen("tcp", net.JoinHostPort(gw, "0"))
	if err != nil {
		t.Fatalf("listen on gateway %s: %v", gw, err)
	}
	t.Cleanup(func() { _ = gwLn.Close() })
	go func() {
		for {
			c, err := gwLn.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	_, gwPort, _ := net.SplitHostPort(gwLn.Addr().String())
	// Control: a bridge-networked container must reach that listener, or a
	// "blocked" gateway result says nothing (a host firewall can refuse it).
	control, _ := exec.Command(rt.(DockerCLI).bin(), "run", "--rm", "--network", "bridge", egressProbeImage,
		"node", "-e", egressProbeJS, "http://127.0.0.1:1/", "host_gateway="+net.JoinHostPort(gw, gwPort)).Output()
	gatewayProven := strings.Contains(string(control), "host_gateway=open")
	if !gatewayProven {
		if os.Getenv("CI") != "" {
			t.Fatalf("control guest on the bridge could not reach the gateway listener: %q", control)
		}
		t.Logf("host gateway refuses bridge guests here (%q); its blocked result is not evidence", control)
	}

	planePort := strconv.Itoa(port)
	targets := []string{
		"public_ip=1.1.1.1:443",
		"public_name=example.com:443",
		"loopback_plane_port=127.0.0.1:" + planePort,
		"host_gateway=" + net.JoinHostPort(gw, gwPort),
		"host_gateway_name=host.docker.internal:" + gwPort,
		"metadata=169.254.169.254:80",
		"ipv6=[2606:4700:4700::1111]:443",
		"github=github.com:443",
	}
	argv := append([]string{"node", "-e", egressProbeJS, "http://" + net.JoinHostPort(PlaneHost, planePort) + "/healthz"}, targets...)
	out, _, err := rt.(DockerCLI).ExecOutput(id, argv)
	if err != nil {
		t.Fatalf("probe: %v %s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	got := strings.Fields(lines[len(lines)-1])
	want := []string{
		"public_ip=blocked", "public_name=blocked", "loopback_plane_port=blocked",
		"host_gateway=blocked", "host_gateway_name=blocked", "metadata=blocked",
		"ipv6=blocked", "github=blocked", "plane=ok",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("guest egress\n got %v\nwant %v", got, want)
	}
	if !gatewayProven {
		t.Skip("egress blocked, but the host gateway result is unproven on this host")
	}
}

// A custom-class guest reaches each listed destination by its name through
// the guest link, and still nothing it does not list (#681). The listed
// name is re-pointed at a local server, so the proof needs no internet.
func TestLiveCustomGuestReachesListedDestinationOnly(t *testing.T) {
	unavailable := t.Skipf
	if os.Getenv("CI") != "" {
		unavailable = t.Fatalf
	}
	rt, err := LookRuntime()
	if err != nil {
		unavailable("container runtime required for live egress proof: %v", err)
		return
	}
	listed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(listed.Close)
	if err := exec.Command(rt.(DockerCLI).bin(), "pull", "-q", egressProbeImage).Run(); err != nil {
		unavailable("cannot pull %s: %v", egressProbeImage, err)
		return
	}
	d := Container{RT: rt, Image: egressProbeImage}
	id, err := d.Create(fmt.Sprintf("egress-custom-%d", time.Now().UnixNano()%1_000_000_000))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "cannot connect") {
			unavailable("container runtime unavailable: %v", err)
			return
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Destroy(id) })
	targets, err := EgressLinkTargets([]string{"registry.example.test:8443"})
	if err != nil {
		t.Fatal(err)
	}
	targets[0].Dial = listed.Listener.Addr().String()
	link, err := StartLink(d, id, targets)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(link.Close)

	// The probe's "plane" fetch goes to the listed name here.
	argv := []string{"node", "-e", egressProbeJS, "http://registry.example.test:8443/",
		"unlisted_name=example.com:443", "unlisted_port=registry.example.test:443", "public_ip=1.1.1.1:443"}
	out, _, err := rt.(DockerCLI).ExecOutput(id, argv)
	if err != nil {
		t.Fatalf("probe: %v %s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	got := strings.Join(strings.Fields(lines[len(lines)-1]), " ")
	if want := "unlisted_name=blocked unlisted_port=blocked public_ip=blocked plane=ok"; got != want {
		t.Fatalf("guest egress\n got %s\nwant %s", got, want)
	}
}

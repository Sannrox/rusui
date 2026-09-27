package env

import "testing"

func TestContainerCreateIsTheSupportedIsolationProfile(t *testing.T) {
	rt := &FakeRuntime{}
	c := Container{RT: rt, Image: "rusui-guest:test"}
	if c.Kind() != KindContainer {
		t.Fatalf("kind %q", c.Kind())
	}
	id, err := c.Create("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("empty handle")
	}
	if len(rt.Created) != 1 {
		t.Fatalf("created %d", len(rt.Created))
	}
	spec := rt.Created[0]
	if spec.Network != TrustedNetwork {
		t.Fatalf("network %q", spec.Network)
	}
	if !spec.DisableIPv6 {
		t.Fatal("ipv6 still enabled")
	}
	if err := GuestDialAllowed(PlaneHost, false); err != nil {
		t.Fatal(err)
	}
	if err := GuestDialAllowed("github.com", false); err == nil {
		t.Fatal("guest must not dial off-plane")
	}
	p := Process{Root: t.TempDir()}
	if p.Kind() != KindProcess {
		t.Fatalf("process kind %q", p.Kind())
	}
	if KindContainer == KindProcess {
		t.Fatal("container and process must remain distinct kinds")
	}
}

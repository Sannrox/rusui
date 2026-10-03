package ops

import (
	"testing"

	"github.com/sannrox/rusui/internal/env"
)

func TestCheckGuestLinkReportsWhatTheGuestReached(t *testing.T) {
	getenv := func(k string) string {
		return map[string]string{"RUSUI_GUEST_IMAGE": "rusui-guest:test", "RUSUI_PLANE_CA": "/ca.crt"}[k]
	}
	for _, tc := range []struct {
		out, status string
	}{
		{"plane=ok direct=blocked", StatusReady},
		{"plane=ok direct=open", StatusMisconfigured},
		{"plane=fail direct=blocked", StatusUnavailable},
		{"Warning: Ignoring extra certs\nplane=ok direct=blocked", StatusReady},
		{"Warning: plane=ok direct=blocked\nplane=fail direct=blocked", StatusUnavailable},
	} {
		rt := &env.FakeRuntime{OutputHook: func(string, []string) []byte { return []byte(tc.out + "\n") }}
		c := CheckGuestLink(rt, getenv, "https://127.0.0.1:8282")
		if c.Status != tc.status || !c.Blocker {
			t.Fatalf("%s: %+v", tc.out, c)
		}
		if len(rt.Created) != 1 || rt.Created[0].Network != env.NoNetwork || len(rt.Links) != 1 || len(rt.Removed) != 1 {
			t.Fatalf("%s: created %+v links %d removed %d", tc.out, rt.Created, len(rt.Links), len(rt.Removed))
		}
	}
	if c := CheckGuestLink(&env.FakeRuntime{}, func(string) string { return "" }, "https://127.0.0.1:8282"); c.Blocker {
		t.Fatalf("unconfigured %+v", c)
	}
}

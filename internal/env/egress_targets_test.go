package env

import "testing"

// Two ports of one host share the guest address the host name resolves to;
// a second host gets its own, or one name would reach the other's target.
func TestEgressLinkTargetsShareAddressPerHost(t *testing.T) {
	got, err := EgressLinkTargets([]string{"a.example:443", "a.example:8443", "b.example:443"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].GuestIP != got[1].GuestIP || got[0].GuestIP == got[2].GuestIP ||
		got[1].Port != 8443 || got[1].Dial != "a.example:8443" || got[2].Host != "b.example" {
		t.Fatalf("targets %+v", got)
	}
}

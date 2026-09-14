package acp

import "strings"

// Decision is the plane's answer to session/request_permission.
type Decision struct {
	Matched bool
	Allow   bool
}

// PermissionGate maps a permission request onto policy. A request with
// no match is denied and surfaced as an approval.
type PermissionGate interface {
	Decide(PermissionParams) Decision
}

// DenyUnmatched refuses every request. v1 policy.yaml has no ACP tool
// rules, so this is the production gate.
type DenyUnmatched struct{}

func (DenyUnmatched) Decide(PermissionParams) Decision {
	return Decision{Matched: false, Allow: false}
}

func pickOption(opts []PermOption, allow bool) string {
	want := []string{"reject-once", "reject_once", "deny"}
	if allow {
		want = []string{"allow-once", "allow_once", "allow-always", "allow"}
	}
	for _, o := range opts {
		id := strings.ToLower(o.OptionID)
		kind := strings.ToLower(o.Kind)
		for _, w := range want {
			if id == w || kind == w || strings.Contains(id, w) || strings.Contains(kind, w) {
				return o.OptionID
			}
		}
	}
	if len(opts) > 0 {
		return opts[0].OptionID
	}
	if allow {
		return "allow-once"
	}
	return "reject-once"
}

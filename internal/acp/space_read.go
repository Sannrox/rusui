package acp

import "strings"

// ReadKinds are ACP tool kinds treated as read-only or stateless.
var ReadKinds = map[string]bool{
	"read":   true,
	"search": true,
	"fetch":  true,
	"think":  true,
}

const ToolWrite = "write"

// WriteCapable reports whether a permission request can change workspace
// or remote state. Missing kind is treated as execute.
func WriteCapable(p PermissionParams) bool {
	return !ReadKinds[ToolKind(p)]
}

func ToolKind(p PermissionParams) string {
	_, kind, _ := toolCallFields(p.ToolCall)
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return "execute"
	}
	return kind
}

func hasToolGrant(grants []string, kind string) bool {
	kind = strings.ToLower(strings.TrimSpace(kind))
	for _, g := range grants {
		switch strings.ToLower(strings.TrimSpace(g)) {
		case ToolWrite, kind:
			return true
		}
	}
	return false
}

// SpaceReadGate denies write-capable tools on a space-mode session until
// a live per-session grant exists (#608). Read kinds fall through to Next.
// Live, when set, is consulted on every Decide so a revoke during the turn
// takes effect; a failed lookup denies writes.
type SpaceReadGate struct {
	Next   PermissionGate
	Grants []string
	Live   func() ([]string, error)
}

func (g SpaceReadGate) Decide(p PermissionParams) Decision {
	grants := g.Grants
	if g.Live != nil {
		live, err := g.Live()
		if err != nil {
			if WriteCapable(p) {
				return Decision{Matched: true, Allow: false}
			}
			live = nil
		}
		grants = live
	}
	if WriteCapable(p) && !hasToolGrant(grants, ToolKind(p)) {
		return Decision{Matched: true, Allow: false}
	}
	if g.Next == nil {
		return DenyUnmatched{}.Decide(p)
	}
	return g.Next.Decide(p)
}

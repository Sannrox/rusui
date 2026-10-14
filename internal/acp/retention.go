package acp

// RetentionWriteGate denies write-capable tools on an indefinite
// session until a live audited override exists (#609). Short-lived
// sessions and read kinds fall through to Next. Empty class is
// treated as short so callers that omit retention keep working.
// Live, when set, is consulted on every Decide; a failed lookup
// denies writes.
type RetentionWriteGate struct {
	Next     PermissionGate
	Class    string
	Override bool
	Live     func() (class string, override bool, err error)
}

func (g RetentionWriteGate) Decide(p PermissionParams) Decision {
	class, override := g.Class, g.Override
	if g.Live != nil {
		liveClass, liveOverride, err := g.Live()
		if err != nil {
			if WriteCapable(p) {
				return Decision{Matched: true, Allow: false}
			}
		} else {
			class, override = liveClass, liveOverride
		}
	}
	if !indefiniteRetention(class) {
		return g.next(p)
	}
	if !WriteCapable(p) {
		return g.next(p)
	}
	if override {
		return g.next(p)
	}
	return Decision{Matched: true, Allow: false}
}

func (g RetentionWriteGate) next(p PermissionParams) Decision {
	if g.Next == nil {
		return DenyUnmatched{}.Decide(p)
	}
	return g.Next.Decide(p)
}

func indefiniteRetention(class string) bool {
	switch class {
	case "indefinite":
		return true
	default:
		return false
	}
}

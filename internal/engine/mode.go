package engine

// Guest session mode catalog forwarded on ACP session/new. Empty omits
// the field so today's spawn stays unchanged.
const (
	SessionModeLow    = "low"
	SessionModeMedium = "medium"
	SessionModeHigh   = "high"
	SessionModeUltra  = "ultra"
)

// ValidSessionMode reports whether name is empty (omit) or a catalog name.
func ValidSessionMode(name string) bool {
	switch name {
	case "", SessionModeLow, SessionModeMedium, SessionModeHigh, SessionModeUltra:
		return true
	default:
		return false
	}
}

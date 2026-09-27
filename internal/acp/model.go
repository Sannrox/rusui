package acp

// ValidGuestModel reports whether id can be copied into a guest environment.
// An empty id means the operator did not name one. A value with shell or
// header metacharacters is rejected so it cannot escape a single env entry.
func ValidGuestModel(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-' || r == '/' || r == ':':
		default:
			return false
		}
	}
	return true
}

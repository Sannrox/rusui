package engine

import "github.com/sannrox/rusui/internal/store"

const (
	SizeSmall  = "small"
	SizeMedium = "medium"
	SizeLarge  = "large"
)

const (
	sizeSmallCPU    = 1000
	sizeMediumCPU   = 2000
	sizeLargeCPU    = 4000
	sizeSmallMemory = 2 << 30
	sizeMediumMem   = 4 << 30
	sizeLargeMemory = 8 << 30
)

// NormalizeSize maps a session or project size name to small, medium, or
// large. Empty becomes medium. Unknown names stay empty so callers can refuse.
func NormalizeSize(name string) string {
	switch name {
	case SizeSmall, SizeMedium, SizeLarge:
		return name
	case "":
		return SizeMedium
	default:
		return ""
	}
}

// SizeLimits is the CPU and memory the container runtime enforces for name.
func SizeLimits(name string) (cpuMillis int, memoryBytes int64) {
	switch NormalizeSize(name) {
	case SizeSmall:
		return sizeSmallCPU, sizeSmallMemory
	case SizeLarge:
		return sizeLargeCPU, sizeLargeMemory
	default:
		return sizeMediumCPU, sizeMediumMem
	}
}

func (e *Engine) SessionSize(sess *store.Session) string {
	if sess != nil && sess.Size != "" {
		if n := NormalizeSize(sess.Size); n != "" {
			return n
		}
	}
	if sess != nil {
		if p, ok := e.PolicySnapshot().Project(sess.Project); ok && p.Size != "" {
			return NormalizeSize(p.Size)
		}
		if p, ok := e.PolicySnapshot().ProjectForRepo(sess.Repo); ok && p.Size != "" {
			return NormalizeSize(p.Size)
		}
	}
	return SizeMedium
}

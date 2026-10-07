package engine

import "github.com/sannrox/rusui/internal/store"

// Guest content bounds match the pinned HTTP guest
// (shikigami MAX_CONTENT_PART_BYTES / MAX_CONTENT_AGGREGATE_BYTES).
const (
	GuestContentPartBytes      = 8 << 20
	GuestContentAggregateBytes = 16 << 20
	GuestContentMaxParts       = 32
	// GuestContentPromptOverheadBytes is room the runner adds around the
	// operator prompt (follow-up title, result instructions, published-PR
	// reminder) so an admitted request still fits the guest part and
	// aggregate bounds.
	GuestContentPromptOverheadBytes = 4 << 10
)

// GuestACPPart returns the ACP prompt part type and canonical media type
// the pinned HTTP guest can read. The plane refuses every other media type.
func GuestACPPart(mime string) (typ, canonical string, ok bool) {
	canonical = store.CanonicalMIME(mime)
	switch canonical {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return "image", canonical, true
	case "application/pdf", "text/plain", "text/markdown":
		return "resource", canonical, true
	default:
		return "", canonical, false
	}
}

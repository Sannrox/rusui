package slack

import (
	"sync"
	"time"
)

// ExceptionQuiet is how long an identical exception message stays muted
// in Slack after it was posted. The plane log keeps every occurrence.
const ExceptionQuiet = time.Hour

// Throttle mutes a repeated message for Window after it was let through,
// so a retry loop or a scheduler tick that hits the same failure posts
// once. A different message passes at once. State is per process: after
// a restart a muted message can post once more.
type Throttle struct {
	Window time.Duration
	Now    func() time.Time

	mu   sync.Mutex
	sent map[string]time.Time
}

// Allow reports whether msg should be posted now, and records it if so.
func (t *Throttle) Allow(msg string) bool {
	now := time.Now()
	if t.Now != nil {
		now = t.Now()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sent == nil {
		t.sent = map[string]time.Time{}
	}
	if at, ok := t.sent[msg]; ok && now.Sub(at) < t.Window {
		return false
	}
	// Drop expired entries so the map stays bounded by distinct messages
	// seen within one window.
	for m, at := range t.sent {
		if now.Sub(at) >= t.Window {
			delete(t.sent, m)
		}
	}
	t.sent[msg] = now
	return true
}

// Forget unmutes msg, for a post that failed and should be retried on
// the next occurrence.
func (t *Throttle) Forget(msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.sent, msg)
}

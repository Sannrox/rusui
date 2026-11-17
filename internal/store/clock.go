package store

import (
	"time"
)

// Now reads the store's clock. Instances anchor their own clocks to it so
// that every lease expiry they write or compare sits on one timeline
// (ADR 0089 D4).
func Now(s *Store) (time.Time, error) {
	if s.pg {
		var t time.Time
		err := s.DB.QueryRow(`SELECT clock_timestamp()`).Scan(&t)
		return t.UTC(), err
	}
	var raw string
	if err := s.DB.QueryRow(`SELECT strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`).Scan(&raw); err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, raw)
}

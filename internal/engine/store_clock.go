package engine

import (
	"sync/atomic"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

// StoreClock is an instance clock anchored to the store's clock (ADR 0089
// D4). Every lease, deadline and expiry the engine writes or compares then
// sits on the store's timeline, so skew between instances cannot extend or
// shorten a lease. The error left is half a sync round trip plus local drift
// between syncs. Sync it at start and on each scheduler renewal.
type StoreClock struct {
	store  *store.Store
	local  func() time.Time
	offset atomic.Int64 // store minus local, in nanoseconds
}

// NewStoreClock returns a clock that reads local time until the first Sync.
func NewStoreClock(s *store.Store) *StoreClock {
	return &StoreClock{store: s, local: time.Now}
}

// Sync measures the offset between the store's clock and this instance's.
func (c *StoreClock) Sync() error {
	before := c.local()
	at, err := store.Now(c.store)
	if err != nil {
		return err
	}
	after := c.local()
	c.offset.Store(int64(at.Sub(before.Add(after.Sub(before) / 2))))
	return nil
}

func (c *StoreClock) Now() time.Time {
	return c.local().Add(time.Duration(c.offset.Load())).UTC()
}

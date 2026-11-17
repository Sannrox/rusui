package engine

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

// Scheduler lease timing (ADR 0089 D6, values from #698).
const (
	SchedulerLeaseTTL   = 2 * time.Minute
	SchedulerRenewEvery = 40 * time.Second
)

// SchedulerLease decides whether this plane instance runs the plane-wide
// timers. Call Renew every SchedulerRenewEvery and run a job only while Held
// reports true, fencing its writes with the returned lease. Not safe for
// concurrent use; runScheduler owns it.
type SchedulerLease struct {
	store  *store.Store
	holder string
	lease  store.PlaneLease
	until  time.Time // local deadline, never later than the store's expiry
}

// NewSchedulerLease returns a lease handle with a fresh holder id for this
// process.
func NewSchedulerLease(s *store.Store) *SchedulerLease {
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	return &SchedulerLease{store: s, holder: hex.EncodeToString(raw)}
}

// Renew claims or renews the scheduler lease. Losing it, or failing to reach
// the store, stops Held at once.
func (l *SchedulerLease) Renew() error {
	start := time.Now()
	got, ok, err := store.ClaimPlaneLease(l.store, store.SchedulerLease, l.holder, SchedulerLeaseTTL)
	if err != nil || !ok {
		l.until = time.Time{}
		return err
	}
	l.lease = got
	// The store rounds its clock down to seconds, so expiry can land up to a
	// second before start+TTL.
	l.until = start.Add(SchedulerLeaseTTL - time.Second)
	return nil
}

// Held returns the lease to fence with while this instance holds it.
func (l *SchedulerLease) Held() (store.PlaneLease, bool) {
	return l.lease, time.Now().Before(l.until)
}

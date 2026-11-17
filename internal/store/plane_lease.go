package store

import (
	"database/sql"
	"errors"
	"time"
)

// SchedulerLease names the one lease that gates every plane-wide timer
// (ADR 0089 D6).
const SchedulerLease = "scheduler"

// ErrPlaneLeaseLost is returned when a fenced write runs after its lease
// was renewed by nobody, expired, or taken over.
var ErrPlaneLeaseLost = errors.New("plane job lease lost")

// PlaneLease is one holder's claim on a plane-wide lease row, as returned by
// ClaimPlaneLease. Fence writes with Check.
type PlaneLease struct {
	Name       string
	Holder     string
	Generation int64
}

// Lease expiry compares against unixepoch(), the store's clock in unix
// seconds, in the same statement, never an instance clock (ADR 0089 D4).
// SQLite has unixepoch() natively; the PostgreSQL baseline defines it.

// ClaimPlaneLease claims or renews the named lease for holder for ttl. A
// renewal by the live holder keeps the generation; a claim of an expired
// row, including the holder's own, increments it. It reports false while
// another holder's lease is live.
func ClaimPlaneLease(s *Store, name, holder string, ttl time.Duration) (PlaneLease, bool, error) {
	l := PlaneLease{Name: name, Holder: holder}
	ok := false
	err := s.Tx(func(tx *sql.Tx) error {
		ok = false // Tx may rerun this callback on PostgreSQL
		if _, err := tx.Exec(`INSERT INTO plane_job_leases(name, holder, generation, expires_at) VALUES (?, '', 0, 0) ON CONFLICT DO NOTHING`, name); err != nil {
			return err
		}
		res, err := tx.Exec(`UPDATE plane_job_leases SET
  generation = CASE WHEN holder = ? AND expires_at > unixepoch() THEN generation ELSE generation + 1 END,
  holder = ?,
  expires_at = unixepoch() + ?
WHERE name = ? AND (holder = ? OR expires_at <= unixepoch())`,
			holder, holder, int64(ttl/time.Second), name, holder)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return nil // another holder's lease is live
		}
		ok = true
		return tx.QueryRow(`SELECT generation FROM plane_job_leases WHERE name = ?`, name).Scan(&l.Generation)
	})
	return l, ok, err
}

// Check fails with ErrPlaneLeaseLost unless l is still the live lease. Call it
// inside the transaction it fences, so the write commits only under the lease.
func (l PlaneLease) Check(tx *sql.Tx) error {
	var one int
	err := tx.QueryRow(`SELECT 1 FROM plane_job_leases
WHERE name = ? AND holder = ? AND generation = ? AND expires_at > unixepoch()`,
		l.Name, l.Holder, l.Generation).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPlaneLeaseLost
	}
	return err
}

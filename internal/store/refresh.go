package store

import (
	"database/sql"
	"time"
)

// refresh_requests is the per-item queue the engine drains to fetch a fresh
// GitHub snapshot. A row is owned (owner=1) by one refresh step at a time;
// generation fences a stale owner out after expiry or restart.

// RefreshOwner is an owned refresh row as ExpireRefreshOwners sees it.
// OwnerExpiresAt is empty when the column is NULL.
type RefreshOwner struct {
	Repo           string
	Item           int
	RetryCount     int
	OwnerExpiresAt string
}

// QueuedRefresh is the next refresh row a step may take.
type QueuedRefresh struct {
	Repo       string
	Item       int
	ItemKind   string
	Generation int
	Force      bool
}

// RefreshOwnership is the fencing state of one refresh row. Owner,
// NeedsAnother and Force are the raw 0/1 column values.
type RefreshOwnership struct {
	Generation   int
	Owner        int
	NeedsAnother int
	Force        int
}

// RefreshStatus is the visible state of one refresh row.
type RefreshStatus struct {
	State      string
	Owner      int
	RetryCount int
}

// ListRefreshOwnersTx returns every currently owned refresh row.
func ListRefreshOwnersTx(tx *sql.Tx) ([]RefreshOwner, error) {
	rows, err := tx.Query(`SELECT repo, item, retry_count, owner_expires_at FROM refresh_requests WHERE owner=1`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var list []RefreshOwner
	for rows.Next() {
		var r RefreshOwner
		var exp sql.NullString
		if err := rows.Scan(&r.Repo, &r.Item, &r.RetryCount, &exp); err != nil {
			return nil, err
		}
		if exp.Valid {
			r.OwnerExpiresAt = exp.String
		}
		list = append(list, r)
	}
	return list, nil
}

// ReleaseExpiredRefreshOwnerTx drops a dead owner, counts the retry, and
// bumps the generation so the old owner can no longer finish.
func ReleaseExpiredRefreshOwnerTx(tx *sql.Tx, repo string, item, retryCount int, state string, notBefore time.Time) error {
	_, err := tx.Exec(`UPDATE refresh_requests SET owner=0, retry_count=?, state=?, not_before=?, generation=generation+1 WHERE repo=? AND item=?`,
		retryCount, state, notBefore.Format(time.RFC3339Nano), repo, item)
	return err
}

// RequeueAllRefreshOwnersTx requeues every owned refresh row at startup; no
// owner survives a restart.
func RequeueAllRefreshOwnersTx(tx *sql.Tx) error {
	_, err := tx.Exec(`UPDATE refresh_requests SET owner=0, state='queued', generation=generation+1 WHERE owner=1`)
	return err
}

// NextQueuedRefreshTx returns one unowned queued refresh row that is due at
// now, optionally limited to repo and item (zero values match any). It
// returns ErrNoRows when nothing is due.
func NextQueuedRefreshTx(tx *sql.Tx, now time.Time, repo string, item int) (QueuedRefresh, error) {
	query := `SELECT repo, item, item_kind, generation, force FROM refresh_requests WHERE state='queued' AND owner=0 AND (not_before IS NULL OR not_before<=?)`
	args := []any{now.Format(time.RFC3339Nano)}
	if repo != "" {
		query += ` AND repo=?`
		args = append(args, repo)
	}
	if item > 0 {
		query += ` AND item=?`
		args = append(args, item)
	}
	query += ` LIMIT 1`
	var r QueuedRefresh
	var f int
	if err := tx.QueryRow(query, args...).Scan(&r.Repo, &r.Item, &r.ItemKind, &r.Generation, &f); err != nil {
		return QueuedRefresh{}, err
	}
	r.Force = f == 1
	return r, nil
}

// TakeRefreshOwnershipTx marks an unowned refresh row running under
// generation until expiresAt.
func TakeRefreshOwnershipTx(tx *sql.Tx, repo string, item, generation int, expiresAt time.Time) error {
	_, err := tx.Exec(`UPDATE refresh_requests SET owner=1, generation=?, owner_expires_at=?, state='running' WHERE repo=? AND item=? AND owner=0`,
		generation, expiresAt.Format(time.RFC3339Nano), repo, item)
	return err
}

// RefreshOwnershipTx reads the fencing state of one refresh row.
func RefreshOwnershipTx(tx *sql.Tx, repo string, item int) (RefreshOwnership, error) {
	var o RefreshOwnership
	err := tx.QueryRow(`SELECT generation, owner, needs_another, force FROM refresh_requests WHERE repo=? AND item=?`, repo, item).Scan(&o.Generation, &o.Owner, &o.NeedsAnother, &o.Force)
	return o, err
}

// RefreshRetryCountTx reads the retry count of one refresh row.
func RefreshRetryCountTx(tx *sql.Tx, repo string, item int) (int, error) {
	var n int
	err := tx.QueryRow(`SELECT retry_count FROM refresh_requests WHERE repo=? AND item=?`, repo, item).Scan(&n)
	return n, err
}

// RefreshGenerationTx reads the generation of one refresh row; ErrNoRows
// when the item has none.
func RefreshGenerationTx(tx *sql.Tx, repo string, item int) (int, error) {
	var gen int
	err := tx.QueryRow(`SELECT generation FROM refresh_requests WHERE repo=? AND item=?`, repo, item).Scan(&gen)
	return gen, err
}

// RefreshStatusOf reads the visible state of one refresh row.
func RefreshStatusOf(s *Store, repo string, item int) (RefreshStatus, error) {
	var r RefreshStatus
	err := s.DB.QueryRow(`SELECT state, owner, retry_count FROM refresh_requests WHERE repo=? AND item=?`, repo, item).Scan(&r.State, &r.Owner, &r.RetryCount)
	return r, err
}

// FailRefreshAttemptTx releases ownership after a failed fetch and records
// the new retry count and state.
func FailRefreshAttemptTx(tx *sql.Tx, repo string, item, retryCount int, state string) error {
	_, err := tx.Exec(`UPDATE refresh_requests SET owner=0, retry_count=?, state=? WHERE repo=? AND item=?`, retryCount, state, repo, item)
	return err
}

// IdleRefreshTx releases ownership and clears any follow-up request.
func IdleRefreshTx(tx *sql.Tx, repo string, item int) error {
	_, err := tx.Exec(`UPDATE refresh_requests SET owner=0, needs_another=0, force=0, state='idle' WHERE repo=? AND item=?`, repo, item)
	return err
}

// DeferRefreshTx releases ownership with an explicit force flag and state,
// clearing needs_another (a refresh denied by the review budget).
func DeferRefreshTx(tx *sql.Tx, repo string, item int, force bool, state string) error {
	_, err := tx.Exec(`UPDATE refresh_requests SET owner=0, needs_another=0, force=?, state=? WHERE repo=? AND item=?`, boolToInt(force), state, repo, item)
	return err
}

// RequeueRefreshTx releases ownership and queues the follow-up that arrived
// while the row was owned.
func RequeueRefreshTx(tx *sql.Tx, repo string, item int) error {
	_, err := tx.Exec(`UPDATE refresh_requests SET owner=0, needs_another=0, force=0, state='queued' WHERE repo=? AND item=?`, repo, item)
	return err
}

// FinishRefreshTx releases ownership after a completed refresh.
func FinishRefreshTx(tx *sql.Tx, repo string, item int) error {
	_, err := tx.Exec(`UPDATE refresh_requests SET owner=0, force=0, state='idle' WHERE repo=? AND item=?`, repo, item)
	return err
}

// ReconcileCheckpointTx reads the last reconciled GitHub delivery of repo.
// Values scanned before an error are still returned.
func ReconcileCheckpointTx(tx *sql.Tx, repo string) (lastDeliveredAt, lastDeliveryID string, err error) {
	err = tx.QueryRow(`SELECT last_delivered_at, last_delivery_id FROM reconcile_checkpoints WHERE repo=?`, repo).Scan(&lastDeliveredAt, &lastDeliveryID)
	return lastDeliveredAt, lastDeliveryID, err
}

// AdvanceReconcileCheckpointTx records deliveryID as the last reconciled
// delivery of repo.
func AdvanceReconcileCheckpointTx(tx *sql.Tx, repo, deliveredAt, deliveryID string) error {
	_, err := tx.Exec(`INSERT INTO reconcile_checkpoints(repo,last_delivered_at,last_delivery_id) VALUES(?,?,?) ON CONFLICT(repo) DO UPDATE SET last_delivered_at=excluded.last_delivered_at, last_delivery_id=excluded.last_delivery_id`, repo, deliveredAt, deliveryID)
	return err
}

// DeliveryFailures reads how often fetching deliveryID has failed.
func DeliveryFailures(s *Store, deliveryID string) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT retries FROM failed_deliveries WHERE delivery_id=?`, deliveryID).Scan(&n)
	return n, err
}

// RecordDeliveryFailuresTx stores the failure count of deliveryID.
func RecordDeliveryFailuresTx(tx *sql.Tx, deliveryID, repo string, retries int) error {
	_, err := tx.Exec(`INSERT INTO failed_deliveries(delivery_id,repo,retries) VALUES(?,?,?) ON CONFLICT(delivery_id) DO UPDATE SET retries=?`, deliveryID, repo, retries, retries)
	return err
}

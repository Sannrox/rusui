package store

import (
	"database/sql"
	"time"
)

// TrackedItem is one (item, item_kind) a repo has a job for.
type TrackedItem struct {
	Item     int
	ItemKind string
}

// ListRepoJobItems returns the item and kind of every job of repo.
func ListRepoJobItems(s *Store, repo string) ([]TrackedItem, error) {
	rows, err := s.DB.Query(`SELECT item, item_kind FROM jobs WHERE repo=?`, repo)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var list []TrackedItem
	for rows.Next() {
		var x TrackedItem
		if err := rows.Scan(&x.Item, &x.ItemKind); err != nil {
			return nil, err
		}
		list = append(list, x)
	}
	return list, nil
}

// ListFailedReviewItems returns the items of repo whose review job failed.
func ListFailedReviewItems(s *Store, repo string) ([]int, error) {
	rows, err := s.DB.Query(`SELECT item FROM jobs WHERE repo=? AND lane='review' AND state='failed'`, repo)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var items []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		items = append(items, n)
	}
	return items, nil
}

// ListReviewJobs returns review jobs ordered by repo and item, limited to
// repo when it is set. Only Repo, Item, State, PendingRevision and
// RetryCount are set.
func ListReviewJobs(s *Store, repo string) ([]Job, error) {
	q := `SELECT repo, item, state, pending_revision, retry_count FROM jobs WHERE lane='review'`
	args := []any{}
	if repo != "" {
		q += ` AND repo=?`
		args = append(args, repo)
	}
	q += ` ORDER BY repo, item`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.Repo, &j.Item, &j.State, &j.PendingRevision, &j.RetryCount); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}

// InsertRetryAuditTx records an operator retry of job j at ts, attributed to by.
func InsertRetryAuditTx(tx *sql.Tx, j *Job, by Attribution, ts time.Time) error {
	_, err := tx.Exec(`INSERT INTO retry_audit (job_id, actor, ts, pending_revision, previous_retry_count, delegated_by) VALUES (?,?,?,?,?,?)`,
		j.ID, by.Actor, ts.Format(time.RFC3339Nano), j.PendingRevision, j.RetryCount, nullable(by.Delegate))
	return err
}

// QueuedTurn is a queued turn a claim may lease.
type QueuedTurn struct {
	ID            int64
	SessionID     int64
	EnvironmentID int64
	Tenant        string
	Project       string
	Act           string
}

// ListQueuedTurnsTx returns queued turns in lanes on unarchived sessions of
// repos within tenant. A queued run is leased before older review or
// scheduled turns; turns in the same class stay in id order.
func ListQueuedTurnsTx(tx *sql.Tx, repos, lanes []string, tenant string) ([]QueuedTurn, error) {
	args := make([]any, 0, len(repos)+len(lanes)+1)
	for _, r := range repos {
		args = append(args, r)
	}
	for _, l := range lanes {
		args = append(args, l)
	}
	args = append(args, tenant)
	rows, err := tx.Query(`SELECT t.id, t.session_id, s.environment_id, COALESCE(s.tenant_id,'default'), COALESCE(s.project,''), COALESCE(s.act,'') FROM turns t JOIN sessions s ON s.id=t.session_id WHERE s.repo IN (`+placeholders(len(repos))+`) AND COALESCE(s.archived, 0)=0 AND t.state='queued' AND t.lane IN (`+placeholders(len(lanes))+`) AND COALESCE(s.tenant_id,'default')=? ORDER BY CASE WHEN t.lane='run' THEN 0 ELSE 1 END, t.id`,
		args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var queued []QueuedTurn
	for rows.Next() {
		var c QueuedTurn
		if err := rows.Scan(&c.ID, &c.SessionID, &c.EnvironmentID, &c.Tenant, &c.Project, &c.Act); err != nil {
			return nil, err
		}
		queued = append(queued, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return queued, rows.Close()
}

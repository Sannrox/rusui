package store

import "database/sql"

// apply_attempts tracks one proposed action from planned through in_flight
// to a terminal state; evidence_invalidations records actions whose evidence
// went stale so a forced refresh can re-review them.

// PendingComment is a live comment apply that has not reached a terminal
// state. CommentID is the GitHub comment id once one is known.
type PendingComment struct {
	ActionID  string
	Body      string
	CommentID int64
}

// ListPendingCommentsTx returns in_flight or uncertain comment applies of
// repo#item.
func ListPendingCommentsTx(tx *sql.Tx, repo string, item int) ([]PendingComment, error) {
	rows, err := tx.Query(`SELECT aa.action_id, aa.github_comment_id, a.body
FROM apply_attempts aa JOIN actions a ON a.action_id=aa.action_id
WHERE aa.repo=? AND aa.item=? AND aa.state IN ('in_flight','uncertain') AND a.action_type='comment'`, repo, item)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var list []PendingComment
	for rows.Next() {
		var p PendingComment
		if err := rows.Scan(&p.ActionID, &p.CommentID, &p.Body); err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	return list, rows.Err()
}

// SettlePendingCommentTx moves an in_flight or uncertain comment apply to
// state after a failed publish.
func SettlePendingCommentTx(tx *sql.Tx, actionID, state string) error {
	_, err := tx.Exec(`UPDATE apply_attempts SET state=? WHERE action_id=? AND state IN ('in_flight','uncertain')`, state, actionID)
	return err
}

// CompletePendingCommentTx marks an in_flight or uncertain comment apply
// succeeded with the GitHub comment id it produced.
func CompletePendingCommentTx(tx *sql.Tx, actionID string, commentID int64) error {
	_, err := tx.Exec(`UPDATE apply_attempts SET state='succeeded', github_comment_id=? WHERE action_id=? AND state IN ('in_flight','uncertain')`, commentID, actionID)
	return err
}

// PlanApplyAttemptTx records actionID as planned unless it already exists.
func PlanApplyAttemptTx(tx *sql.Tx, actionID string, reviewRevisionID int64, repo string, item int) error {
	_, err := tx.Exec(`INSERT INTO apply_attempts (action_id, review_revision_id, repo, item, state) VALUES (?,?,?,?, 'planned') ON CONFLICT DO NOTHING`,
		actionID, reviewRevisionID, repo, item)
	return err
}

// ApplyAttemptStateTx reads the state of actionID; ErrNoRows when unknown.
func ApplyAttemptStateTx(tx *sql.Tx, actionID string) (string, error) {
	var state string
	err := tx.QueryRow(`SELECT state FROM apply_attempts WHERE action_id=?`, actionID).Scan(&state)
	return state, err
}

// CancelApplyAttemptTx marks actionID cancelled.
func CancelApplyAttemptTx(tx *sql.Tx, actionID string) error {
	_, err := tx.Exec(`UPDATE apply_attempts SET state='cancelled' WHERE action_id=?`, actionID)
	return err
}

// StartApplyAttemptTx marks actionID in_flight.
func StartApplyAttemptTx(tx *sql.Tx, actionID string) error {
	_, err := tx.Exec(`UPDATE apply_attempts SET state='in_flight' WHERE action_id=?`, actionID)
	return err
}

// SucceedApplyAttemptTx marks actionID succeeded.
func SucceedApplyAttemptTx(tx *sql.Tx, actionID string) error {
	_, err := tx.Exec(`UPDATE apply_attempts SET state='succeeded' WHERE action_id=?`, actionID)
	return err
}

// CancelRevisionApplyAttemptsTx cancels every apply planned from one review
// revision.
func CancelRevisionApplyAttemptsTx(tx *sql.Tx, reviewRevisionID int64) error {
	_, err := tx.Exec(`UPDATE apply_attempts SET state='cancelled' WHERE review_revision_id=?`, reviewRevisionID)
	return err
}

// InvalidateEvidenceTx records that actionID's evidence no longer holds.
func InvalidateEvidenceTx(tx *sql.Tx, reviewRevisionID int64, actionID string) error {
	_, err := tx.Exec(`INSERT INTO evidence_invalidations (review_revision_id, action_id, consumed) VALUES (?,?,0) ON CONFLICT DO NOTHING`, reviewRevisionID, actionID)
	return err
}

// CountPendingInvalidationsTx counts unconsumed evidence invalidations on
// review revisions of repo#item.
func CountPendingInvalidationsTx(tx *sql.Tx, repo string, item int) (int, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM evidence_invalidations ei JOIN review_revisions r ON r.id=ei.review_revision_id JOIN jobs j ON j.id=r.job_id WHERE j.repo=? AND j.item=? AND ei.consumed=0`, repo, item).Scan(&n)
	return n, err
}

// ConsumeInvalidationsTx marks every pending evidence invalidation of
// repo#item consumed.
func ConsumeInvalidationsTx(tx *sql.Tx, repo string, item int) error {
	_, err := tx.Exec(`UPDATE evidence_invalidations SET consumed=1 WHERE consumed=0 AND review_revision_id IN (SELECT r.id FROM review_revisions r JOIN jobs j ON j.id=r.job_id WHERE j.repo=? AND j.item=?)`, repo, item)
	return err
}

// ApplyAction is the actions row an apply attempt records.
type ApplyAction struct {
	ActionID         string
	TurnID           int64
	ReviewRevisionID int64
	Repo             string
	Item             int
	Type             string
	ReasonCode       string
	EvidenceClass    string
	LimitSentence    string
	Body             string
}

// InsertApplyActionTx records action a on the review session of its item
// (NULL session when none exists), unless the action id already exists.
func InsertApplyActionTx(tx *sql.Tx, a ApplyAction) error {
	var sid sql.NullInt64
	_ = tx.QueryRow(`SELECT id FROM sessions WHERE kind=? AND repo=? AND item=?`, SessionKindReview, a.Repo, a.Item).Scan(&sid)
	if sid.Valid {
		if err := lockSessionTx(tx, sid.Int64); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`INSERT INTO actions (action_id, session_id, turn_id, review_revision_id, repo, item, action_type, reason_code, evidence_class, limit_sentence, body) VALUES (?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`,
		a.ActionID, nullableInt(sid), a.TurnID, a.ReviewRevisionID, a.Repo, a.Item, a.Type, a.ReasonCode, a.EvidenceClass, a.LimitSentence, a.Body)
	return err
}

func nullableInt(n sql.NullInt64) any {
	if !n.Valid {
		return nil
	}
	return n.Int64
}

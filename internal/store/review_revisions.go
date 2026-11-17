package store

import (
	"database/sql"
	"iter"
)

// ReviewRevision rows hold each immutable review artifact a turn produced.

// InsertReviewRevisionTx records the artifact payload a turn completed
// against claimedRevision and returns its id. The row keeps the policy
// revision the turn was claimed under.
func InsertReviewRevisionTx(tx *sql.Tx, jobID int64, claimedRevision int, itemHash, mainSHA, payload string) (int64, error) {
	newID, err := scanID(tx.QueryRow(`INSERT INTO review_revisions (job_id, claimed_revision, item_hash, main_sha, payload, policy_revision_id) VALUES (?,?,?,?,?,(SELECT policy_revision_id FROM turns WHERE id=?)) RETURNING id`,
		jobID, claimedRevision, itemHash, mainSHA, payload, jobID))
	if err != nil {
		return 0, err
	}
	return newID, nil
}

// LatestReviewRevisionTx returns the id and payload of the newest review
// revision of jobID; ErrNoRows when there is none.
func LatestReviewRevisionTx(tx *sql.Tx, jobID int64) (id int64, payload string, err error) {
	err = tx.QueryRow(`SELECT id, payload FROM review_revisions WHERE job_id=? ORDER BY id DESC LIMIT 1`, jobID).Scan(&id, &payload)
	return id, payload, err
}

// JobReviewPayloads yields every review payload of jobID, newest first.
// Breaking out of the loop stops the read.
func JobReviewPayloads(s *Store, jobID int64) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		rows, err := s.DB.Query(`SELECT payload FROM review_revisions WHERE job_id=? ORDER BY id DESC`, jobID)
		eachRow(rows, err, scanString, yield)
	}
}

// SessionReviewPayloads yields every review payload on any turn of
// sessionID, newest first. Breaking out of the loop stops the read.
func SessionReviewPayloads(s *Store, sessionID int64) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		rows, err := s.DB.Query(`SELECT r.payload FROM review_revisions r JOIN turns t ON t.id=r.job_id WHERE t.session_id=? ORDER BY r.id DESC`, sessionID)
		eachRow(rows, err, scanString, yield)
	}
}

func scanString(rows *sql.Rows) (string, error) {
	var v string
	err := rows.Scan(&v)
	return v, err
}

// eachRow yields scanned rows until the consumer stops, then closes rows.
// A query, scan, or iteration error is yielded once and ends the sequence.
func eachRow[T any](rows *sql.Rows, err error, scan func(*sql.Rows) (T, error), yield func(T, error) bool) {
	var zero T
	if err != nil {
		yield(zero, err)
		return
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			yield(zero, err)
			return
		}
		if !yield(v, nil) {
			return
		}
	}
	if err := rows.Err(); err != nil {
		yield(zero, err)
	}
}

package store

import "database/sql"

// FollowUp is one unconsumed entry of a session's FIFO. Queued marks a
// prompt held until the current turn ends; it may still be dropped (#492).
type FollowUp struct {
	Seq    int
	Prompt string
	Queued bool
}

func EnqueueFollowUpTx(tx *sql.Tx, sessionID int64, prompt string) (int, error) {
	return enqueueFollowUpTx(tx, sessionID, prompt, false)
}

// EnqueueQueuedPromptTx appends a prompt that waits for the current turn
// to end and that DropQueuedPromptsTx or a session cancel can drop.
func EnqueueQueuedPromptTx(tx *sql.Tx, sessionID int64, prompt string) (int, error) {
	return enqueueFollowUpTx(tx, sessionID, prompt, true)
}

func enqueueFollowUpTx(tx *sql.Tx, sessionID int64, prompt string, queued bool) (int, error) {
	seq, err := nextFollowUpSeqTx(tx, sessionID)
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(`INSERT INTO followup_queue (session_id, seq, prompt, consumed, queued) VALUES (?,?,?,0,?)`, sessionID, seq, prompt, queued)
	return seq, err
}

func nextFollowUpSeqTx(tx *sql.Tx, sessionID int64) (int, error) {
	var seq int
	err := tx.QueryRow(`SELECT IFNULL(MAX(seq), 0) FROM (
  SELECT seq FROM followup_queue WHERE session_id=?
  UNION ALL
  SELECT followup_seq AS seq FROM turn_steers WHERE session_id=? AND followup_seq>0
)`, sessionID, sessionID).Scan(&seq)
	return seq + 1, err
}

func insertFollowUpAtTx(tx *sql.Tx, sessionID int64, seq int, prompt string) error {
	_, err := tx.Exec(`INSERT INTO followup_queue (session_id, seq, prompt, consumed) VALUES (?,?,?,0)`, sessionID, seq, prompt)
	return err
}

func NextFollowUpTx(tx *sql.Tx, sessionID int64) (FollowUp, bool, error) {
	var f FollowUp
	err := tx.QueryRow(`SELECT seq, prompt, queued FROM followup_queue WHERE session_id=? AND consumed=0 ORDER BY seq LIMIT 1`, sessionID).Scan(&f.Seq, &f.Prompt, &f.Queued)
	if err == sql.ErrNoRows {
		return FollowUp{}, false, nil
	}
	if err != nil {
		return FollowUp{}, false, err
	}
	return f, true, nil
}

// DropQueuedPromptsTx consumes every queued prompt not yet started. The
// rows stay, marked dropped, so their sequence numbers are never reused.
func DropQueuedPromptsTx(tx *sql.Tx, sessionID int64) (int, error) {
	res, err := tx.Exec(`UPDATE followup_queue SET consumed=1, dropped=1 WHERE session_id=? AND consumed=0 AND queued=1`, sessionID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func ConsumeFollowUpTx(tx *sql.Tx, sessionID int64, seq int) error {
	_, err := tx.Exec(`UPDATE followup_queue SET consumed=1 WHERE session_id=? AND seq=?`, sessionID, seq)
	return err
}

func SetGuestSessionTx(tx *sql.Tx, sessionID int64, guestID string) error {
	_, err := tx.Exec(`UPDATE sessions SET guest_session_id=? WHERE id=?`, guestID, sessionID)
	return err
}

func GetTurnTx(tx *sql.Tx, id int64) (*Turn, error) {
	var t Turn
	err := tx.QueryRow(`SELECT id, session_id, lane, pending_revision, claimed_revision, lease_generation, retry_count, state FROM turns WHERE id=?`, id).Scan(
		&t.ID, &t.SessionID, &t.Lane, &t.PendingRevision, &t.ClaimedRevision, &t.LeaseGeneration, &t.RetryCount, &t.State)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

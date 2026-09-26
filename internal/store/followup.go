package store

import "database/sql"

func EnqueueFollowUpTx(tx *sql.Tx, sessionID int64, prompt string) (int, error) {
	seq, err := nextFollowUpSeqTx(tx, sessionID)
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(`INSERT INTO followup_queue (session_id, seq, prompt, consumed) VALUES (?,?,?,0)`, sessionID, seq, prompt)
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

func NextFollowUpTx(tx *sql.Tx, sessionID int64) (seq int, prompt string, ok bool, err error) {
	err = tx.QueryRow(`SELECT seq, prompt FROM followup_queue WHERE session_id=? AND consumed=0 ORDER BY seq LIMIT 1`, sessionID).Scan(&seq, &prompt)
	if err == sql.ErrNoRows {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	return seq, prompt, true, nil
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

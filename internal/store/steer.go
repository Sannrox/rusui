package store

import (
	"database/sql"
	"fmt"
)

type Steer struct {
	ID              int64
	SessionID       int64
	TurnID          int64
	LeaseGeneration int
	Prompt          string
}

func EnqueueSteerTx(tx *sql.Tx, sessionID, turnID int64, generation int, prompt string) (int64, error) {
	seq, err := nextFollowUpSeqTx(tx, sessionID)
	if err != nil {
		return 0, err
	}
	res, err := tx.Exec(`INSERT INTO turn_steers (session_id, turn_id, lease_generation, prompt, followup_seq) VALUES (?,?,?,?,?)`, sessionID, turnID, generation, prompt, seq)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func NextSteerTx(tx *sql.Tx, turnID int64, generation int) (*Steer, error) {
	var steer Steer
	err := tx.QueryRow(`SELECT id, session_id, turn_id, lease_generation, prompt FROM turn_steers
WHERE turn_id=? AND lease_generation=? AND acknowledged=0 AND promoted=0 AND received=0 ORDER BY id LIMIT 1`, turnID, generation).Scan(
		&steer.ID, &steer.SessionID, &steer.TurnID, &steer.LeaseGeneration, &steer.Prompt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE turn_steers SET offered=1 WHERE id=? AND offered=0`, steer.ID); err != nil {
		return nil, err
	}
	return &steer, nil
}

func ReceiveSteerTx(tx *sql.Tx, turnID int64, generation int, steerID int64) error {
	res, err := tx.Exec(`UPDATE turn_steers SET received=1 WHERE id=? AND turn_id=? AND lease_generation=? AND offered=1 AND promoted=0`, steerID, turnID, generation)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 1 {
		return nil
	}
	var received int
	err = tx.QueryRow(`SELECT received FROM turn_steers WHERE id=? AND turn_id=? AND lease_generation=? AND offered=1 AND promoted=0`, steerID, turnID, generation).Scan(&received)
	if err != nil {
		return err
	}
	if received == 1 {
		return nil
	}
	return fmt.Errorf("unknown steer receipt")
}

func AckSteerTx(tx *sql.Tx, turnID int64, generation int, steerID int64) error {
	res, err := tx.Exec(`UPDATE turn_steers SET received=1, acknowledged=1 WHERE id=? AND turn_id=? AND lease_generation=? AND offered=1 AND promoted=0`, steerID, turnID, generation)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 1 {
		return nil
	}
	var acknowledged int
	err = tx.QueryRow(`SELECT acknowledged FROM turn_steers WHERE id=? AND turn_id=? AND lease_generation=? AND offered=1 AND promoted=0`, steerID, turnID, generation).Scan(&acknowledged)
	if err != nil {
		return err
	}
	if acknowledged == 1 {
		return nil
	}
	return fmt.Errorf("unknown steer acknowledgement")
}

func HasEarlierUnacknowledgedSteerTx(tx *sql.Tx, sessionID int64, followupSeq int) (bool, error) {
	var exists int
	err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM turn_steers
WHERE session_id=? AND followup_seq<? AND acknowledged=0 AND promoted=0)`, sessionID, followupSeq).Scan(&exists)
	return exists != 0, err
}

// PromoteSteersTx keeps an operator prompt durable if the lease that owned it
// ends before the runner acknowledges delivery.
func PromoteSteersTx(tx *sql.Tx, turnID int64, throughGeneration int) (int, error) {
	rows, err := tx.Query(`SELECT id, session_id, prompt, followup_seq FROM turn_steers
WHERE turn_id=? AND lease_generation<=? AND acknowledged=0 AND promoted=0 ORDER BY followup_seq, id`, turnID, throughGeneration)
	if err != nil {
		return 0, err
	}
	type pending struct {
		id          int64
		sessionID   int64
		prompt      string
		followupSeq int
	}
	var pendingSteers []pending
	for rows.Next() {
		var steer pending
		if err := rows.Scan(&steer.id, &steer.sessionID, &steer.prompt, &steer.followupSeq); err != nil {
			_ = rows.Close()
			return 0, err
		}
		pendingSteers = append(pendingSteers, steer)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, steer := range pendingSteers {
		if err := insertFollowUpAtTx(tx, steer.sessionID, steer.followupSeq, steer.prompt); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`UPDATE turn_steers SET promoted=1 WHERE id=?`, steer.id); err != nil {
			return 0, err
		}
	}
	return len(pendingSteers), nil
}

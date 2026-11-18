package store

import (
	"database/sql"
	"iter"
)

// ActionBodyTx reads the body of action id; ErrNoRows when it does not exist.
func ActionBodyTx(tx *sql.Tx, id string) (string, error) {
	var body string
	err := tx.QueryRow(`SELECT body FROM actions WHERE action_id=?`, id).Scan(&body)
	return body, err
}

// ActionsOfType yields the id and body of every typ action on sessionID,
// newest first. Only ID and Body are set. Breaking out of the loop stops
// the read.
func ActionsOfType(s *Store, sessionID int64, typ string) iter.Seq2[Action, error] {
	return func(yield func(Action, error) bool) {
		rows, err := s.DB.Query(`SELECT action_id, body FROM actions WHERE session_id=? AND action_type=? ORDER BY rowid DESC`, sessionID, typ)
		eachRow(rows, err, func(rows *sql.Rows) (Action, error) {
			var a Action
			err := rows.Scan(&a.ID, &a.Body)
			return a, err
		}, yield)
	}
}

// ActionExistsTx reports whether an action with id is recorded.
func ActionExistsTx(tx *sql.Tx, id string) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM actions WHERE action_id=?`, id).Scan(&n)
	return n > 0, err
}

// TurnActionBodiesTx returns the body of every typ action on turnID.
func TurnActionBodiesTx(tx *sql.Tx, turnID int64, typ string) ([]string, error) {
	rows, err := tx.Query(`SELECT body FROM actions WHERE turn_id=? AND action_type=?`, turnID, typ)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var bodies []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		bodies = append(bodies, b)
	}
	return bodies, rows.Err()
}

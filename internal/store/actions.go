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

// UpdateActionBodyTx replaces the body of action id.
func UpdateActionBodyTx(tx *sql.Tx, id, body string) error {
	_, err := tx.Exec(`UPDATE actions SET body=? WHERE action_id=?`, body, id)
	return err
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

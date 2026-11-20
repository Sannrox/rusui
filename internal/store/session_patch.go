package store

import (
	"database/sql"
	"errors"
)

// PutSessionPatchTx stores operator patch bytes for a run session.
func PutSessionPatchTx(tx *sql.Tx, sessionID int64, body []byte) error {
	if len(body) == 0 {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO session_patches (session_id, body) VALUES (?, ?)`, sessionID, body)
	return err
}

// SessionPatch is the stored operator git patch, or nil when the session
// has none.
func SessionPatch(s *Store, sessionID int64) ([]byte, error) {
	var body []byte
	err := s.DB.QueryRow(`SELECT body FROM session_patches WHERE session_id=?`, sessionID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return body, err
}

package store

import (
	"database/sql"
	"errors"
)

// LatestPolicyPayload returns the last policy revision loaded by the plane.
func LatestPolicyPayload(s *Store) ([]byte, error) {
	var payload string
	err := s.DB.QueryRow(`SELECT payload FROM policy_revisions ORDER BY id DESC LIMIT 1`).Scan(&payload)
	if err != nil {
		return nil, err
	}
	return []byte(payload), nil
}

// LatestPolicyRevision returns the id and hash of the last loaded policy.
// ok is false when the plane has never stored a revision.
func LatestPolicyRevision(s *Store) (id int64, hash string, ok bool, err error) {
	err = s.DB.QueryRow(`SELECT id, hash FROM policy_revisions ORDER BY id DESC LIMIT 1`).Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	return id, hash, err == nil, err
}

// SetTurnPolicyRevisionTx binds turnID to the newest recorded revision with
// policy hash, the policy the claim ran under. The binding is NULL when no
// revision with that hash was recorded.
func SetTurnPolicyRevisionTx(tx *sql.Tx, turnID int64, hash string) error {
	_, err := tx.Exec(`UPDATE turns SET policy_revision_id=(SELECT MAX(id) FROM policy_revisions WHERE hash=?) WHERE id=?`, hash, turnID)
	return err
}

// RecordPolicyRevisionTx appends a loaded policy revision.
func RecordPolicyRevisionTx(tx *sql.Tx, hash, payload string) error {
	_, err := tx.Exec(`INSERT INTO policy_revisions(hash,payload) VALUES(?,?)`, hash, payload)
	return err
}

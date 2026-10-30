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

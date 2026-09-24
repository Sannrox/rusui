package store

// LatestPolicyPayload returns the last policy revision loaded by the plane.
func LatestPolicyPayload(s *Store) ([]byte, error) {
	var payload string
	err := s.DB.QueryRow(`SELECT payload FROM policy_revisions ORDER BY id DESC LIMIT 1`).Scan(&payload)
	if err != nil {
		return nil, err
	}
	return []byte(payload), nil
}

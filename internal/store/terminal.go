package store

import (
	"database/sql"
	"errors"
	"time"
)

type TerminalLease struct {
	EnvironmentID int64
	SessionID     int64
	Generation    int
	ExpiresAt     time.Time
}

func GetTerminalLease(s *Store, envID int64) (*TerminalLease, bool, error) {
	var l TerminalLease
	var exp string
	err := s.DB.QueryRow(`SELECT environment_id, session_id, generation, expires_at FROM terminal_leases WHERE environment_id=?`, envID).Scan(
		&l.EnvironmentID, &l.SessionID, &l.Generation, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	t, err := time.Parse(time.RFC3339Nano, exp)
	if err != nil {
		return nil, false, err
	}
	l.ExpiresAt = t
	return &l, true, nil
}

func PutTerminalLease(s *Store, l TerminalLease) error {
	_, err := s.DB.Exec(`INSERT OR REPLACE INTO terminal_leases (environment_id, session_id, generation, expires_at) VALUES (?,?,?,?)`,
		l.EnvironmentID, l.SessionID, l.Generation, l.ExpiresAt.UTC().Format(time.RFC3339Nano))
	return err
}

func DeleteTerminalLease(s *Store, envID int64) error {
	_, err := s.DB.Exec(`DELETE FROM terminal_leases WHERE environment_id=?`, envID)
	return err
}

func InsertTerminalAccess(s *Store, envID, sessionID int64, action, detail string) error {
	_, err := s.DB.Exec(`INSERT INTO terminal_access (environment_id, session_id, action, detail, created_at) VALUES (?,?,?,?,?)`,
		envID, sessionID, action, detail, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func CountTerminalAccess(s *Store, envID int64, action string) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM terminal_access WHERE environment_id=? AND action=?`, envID, action).Scan(&n)
	return n, err
}

package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

type PreviewGrant struct {
	TokenHash     string
	SessionID     int64
	EnvironmentID int64
	Handle        string
	Port          int
	ExpiresAt     time.Time
	Revoked       bool
}

func HashPreviewToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func PutPreviewGrant(s *Store, g PreviewGrant) error {
	rev := 0
	if g.Revoked {
		rev = 1
	}
	res, err := s.DB.Exec(`INSERT INTO preview_grants (token_hash, session_id, environment_id, handle, port, expires_at, revoked)
SELECT ?, ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM environments WHERE id=? AND state=?)`,
		g.TokenHash, g.SessionID, g.EnvironmentID, g.Handle, g.Port, g.ExpiresAt.UTC().Format(time.RFC3339Nano), rev, g.EnvironmentID, EnvReady)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrEnvironmentUnavailable
	}
	return err
}

func GetPreviewGrant(s *Store, tokenHash string) (*PreviewGrant, bool, error) {
	var g PreviewGrant
	var exp string
	var rev int
	err := s.DB.QueryRow(`SELECT token_hash, session_id, environment_id, handle, port, expires_at, revoked FROM preview_grants WHERE token_hash=?`, tokenHash).Scan(
		&g.TokenHash, &g.SessionID, &g.EnvironmentID, &g.Handle, &g.Port, &exp, &rev)
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
	g.ExpiresAt = t
	g.Revoked = rev != 0
	return &g, true, nil
}

func RevokePreviewGrant(s *Store, tokenHash string) error {
	_, err := s.DB.Exec(`UPDATE preview_grants SET revoked=1 WHERE token_hash=?`, tokenHash)
	return err
}

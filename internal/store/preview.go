package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

const (
	PreviewClassOperator = "operator"
	PreviewClassShare    = "share"
	ShareTTL1h           = "1h"
	ShareTTL4h           = "4h"
	ShareTTL24h          = "24h"
)

// ShareTTLOptions are the fixed share-grant lifetimes under the 24-hour ceiling.
var ShareTTLOptions = []string{ShareTTL1h, ShareTTL4h, ShareTTL24h}

type PreviewGrant struct {
	TokenHash          string
	SessionID          int64
	EnvironmentID      int64
	Handle             string
	Port               int
	ServiceName        string
	ExpiresAt          time.Time
	Revoked            bool
	Class              string
	OperatorGeneration int
}

func HashPreviewToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// ParseShareTTL returns a lifetime from the fixed share options. Empty is 1h.
func ParseShareTTL(raw string) (time.Duration, bool) {
	switch raw {
	case "", ShareTTL1h:
		return time.Hour, true
	case ShareTTL4h:
		return 4 * time.Hour, true
	case ShareTTL24h:
		return 24 * time.Hour, true
	default:
		return 0, false
	}
}

func previewClass(class string) string {
	if class == PreviewClassShare {
		return PreviewClassShare
	}
	return PreviewClassOperator
}

func PutPreviewGrant(s *Store, g PreviewGrant) error {
	rev := 0
	if g.Revoked {
		rev = 1
	}
	var serviceName any
	if g.ServiceName != "" {
		serviceName = g.ServiceName
	}
	res, err := s.DB.Exec(`INSERT INTO preview_grants (token_hash, session_id, environment_id, handle, port, expires_at, revoked, service_name, grant_class, operator_generation)
SELECT ?, CAST(? AS BIGINT), CAST(? AS BIGINT), ?, CAST(? AS BIGINT), ?, CAST(? AS BIGINT), ?, ?, CAST(? AS BIGINT) WHERE EXISTS (SELECT 1 FROM environments WHERE id=? AND state=?)`,
		g.TokenHash, g.SessionID, g.EnvironmentID, g.Handle, g.Port, g.ExpiresAt.UTC().Format(time.RFC3339Nano), rev, serviceName, previewClass(g.Class), g.OperatorGeneration, g.EnvironmentID, EnvReady)
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
	var serviceName sql.NullString
	var class sql.NullString
	err := s.DB.QueryRow(`SELECT token_hash, session_id, environment_id, handle, port, expires_at, revoked, service_name, COALESCE(grant_class, ?), COALESCE(operator_generation, 0) FROM preview_grants WHERE token_hash=?`, PreviewClassOperator, tokenHash).Scan(
		&g.TokenHash, &g.SessionID, &g.EnvironmentID, &g.Handle, &g.Port, &exp, &rev, &serviceName, &class, &g.OperatorGeneration)
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
	g.ServiceName = serviceName.String
	g.Class = previewClass(class.String)
	return &g, true, nil
}

func RevokePreviewGrant(s *Store, tokenHash string) error {
	_, err := s.DB.Exec(`UPDATE preview_grants SET revoked=1 WHERE token_hash=?`, tokenHash)
	return err
}

// ListShareGrants returns unexpired, unrevoked share grants for a session.
func ListShareGrants(s *Store, sessionID int64, now time.Time) ([]PreviewGrant, error) {
	rows, err := s.DB.Query(`SELECT token_hash, session_id, environment_id, handle, port, expires_at, revoked, service_name, grant_class, operator_generation
FROM preview_grants WHERE session_id=? AND grant_class=? AND revoked=0 AND expires_at>? ORDER BY expires_at, token_hash`,
		sessionID, PreviewClassShare, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PreviewGrant
	for rows.Next() {
		var g PreviewGrant
		var exp string
		var rev int
		var serviceName sql.NullString
		if err := rows.Scan(&g.TokenHash, &g.SessionID, &g.EnvironmentID, &g.Handle, &g.Port, &exp, &rev, &serviceName, &g.Class, &g.OperatorGeneration); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339Nano, exp)
		if err != nil {
			return nil, err
		}
		g.ExpiresAt = t
		g.Revoked = rev != 0
		g.ServiceName = serviceName.String
		out = append(out, g)
	}
	return out, rows.Err()
}

// RevokeShareGrant ends one live share grant on a session. Unknown or already
// revoked hashes return ErrNoRows.
func RevokeShareGrant(s *Store, sessionID int64, tokenHash string) error {
	res, err := s.DB.Exec(`UPDATE preview_grants SET revoked=1 WHERE session_id=? AND token_hash=? AND grant_class=? AND revoked=0`,
		sessionID, tokenHash, PreviewClassShare)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNoRows
	}
	return nil
}

// RevokeShareGrantsForSession ends every live share grant on a session.
func RevokeShareGrantsForSession(s *Store, sessionID int64) error {
	_, err := s.DB.Exec(`UPDATE preview_grants SET revoked=1 WHERE session_id=? AND grant_class=? AND revoked=0`,
		sessionID, PreviewClassShare)
	return err
}

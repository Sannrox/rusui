package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

var (
	ErrGrantExists    = errors.New("grant already exists")
	ErrUnknownGrant   = errors.New("unknown grant")
	ErrGrantSelf      = errors.New("tenant already owns the session")
	ErrInvalidGrantee = errors.New("invalid grantee tenant")
)

type SessionAccessGrant struct {
	ID            int64      `json:"id"`
	SessionID     int64      `json:"session_id"`
	GranteeTenant string     `json:"tenant"`
	CreatedBy     string     `json:"created_by"`
	CreatedAt     time.Time  `json:"created_at"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
}

func SessionGranted(s *Store, sessionID int64, tenant string) (bool, error) {
	var one int
	err := s.DB.QueryRow(`SELECT 1 FROM session_access_grants WHERE session_id=? AND grantee_tenant=? AND revoked_at IS NULL`, sessionID, NormalizeTenantID(tenant)).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func GrantSessionAccess(s *Store, sessionID int64, grantee, createdBy string) (SessionAccessGrant, error) {
	grantee = NormalizeTenantID(grantee)
	createdBy = NormalizeTenantID(createdBy)
	if grantee == "" {
		return SessionAccessGrant{}, ErrInvalidGrantee
	}
	sess, err := GetSession(s, sessionID)
	if err != nil {
		return SessionAccessGrant{}, err
	}
	if NormalizeTenantID(sess.TenantID) == grantee {
		return SessionAccessGrant{}, ErrGrantSelf
	}
	now := time.Now().UTC()
	res, err := s.DB.Exec(`INSERT INTO session_access_grants (session_id, grantee_tenant, created_by, created_at) VALUES (?,?,?,?)`,
		sessionID, grantee, createdBy, now.Format(time.RFC3339Nano))
	if err != nil {
		if strings.Contains(strings.ToUpper(err.Error()), "UNIQUE") {
			return SessionAccessGrant{}, ErrGrantExists
		}
		return SessionAccessGrant{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return SessionAccessGrant{}, err
	}
	return SessionAccessGrant{ID: id, SessionID: sessionID, GranteeTenant: grantee, CreatedBy: createdBy, CreatedAt: now}, nil
}

func RevokeSessionAccess(s *Store, sessionID int64, grantee string) error {
	res, err := s.DB.Exec(`UPDATE session_access_grants SET revoked_at=? WHERE session_id=? AND grantee_tenant=? AND revoked_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339Nano), sessionID, NormalizeTenantID(grantee))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrUnknownGrant
	}
	return nil
}

func ListSessionAccessGrants(s *Store, sessionID int64) ([]SessionAccessGrant, error) {
	rows, err := s.DB.Query(`SELECT id, session_id, grantee_tenant, created_by, created_at FROM session_access_grants WHERE session_id=? AND revoked_at IS NULL ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []SessionAccessGrant{}
	for rows.Next() {
		var g SessionAccessGrant
		var created string
		if err := rows.Scan(&g.ID, &g.SessionID, &g.GranteeTenant, &g.CreatedBy, &created); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
			g.CreatedAt = t
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

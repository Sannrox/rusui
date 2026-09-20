package store

import (
	"database/sql"
	"errors"
	"time"
)

const (
	GrantPrepare = "prepare"
	GrantTurn    = "turn"
)

// Grant is a hashed plane credential. Guests hold only the raw token.
type Grant struct {
	TokenHash string
	Kind      string
	SessionID int64
	TurnID    int64
	Repo      string
	CanPush   bool
	ExpiresAt time.Time
}

func PutGrant(s *Store, g Grant) error {
	push := 0
	if g.CanPush {
		push = 1
	}
	_, err := s.DB.Exec(`INSERT OR REPLACE INTO credential_grants (token_hash, kind, session_id, turn_id, repo, can_push, expires_at) VALUES (?,?,?,?,?,?,?)`,
		g.TokenHash, g.Kind, g.SessionID, g.TurnID, g.Repo, push, g.ExpiresAt.UTC().Format(time.RFC3339Nano))
	return err
}

func DeleteGrantsForTurn(s *Store, turnID int64) error {
	_, err := s.DB.Exec(`DELETE FROM credential_grants WHERE turn_id=?`, turnID)
	return err
}

func RenewGrantTx(tx *sql.Tx, turnID int64, expiresAt string) error {
	_, err := tx.Exec(`UPDATE credential_grants SET expires_at=? WHERE turn_id=?`, expiresAt, turnID)
	return err
}

// LookupGrant returns a live grant. Turn grants require a currently leased
// turn; prepare grants expire only by TTL. Cross-session use is the caller's
// repo/session check against Grant.Repo / Grant.SessionID.
func LookupGrant(s *Store, tokenHash string, now time.Time) (*Grant, bool, error) {
	var g Grant
	var push int
	var exp, turnState string
	err := s.DB.QueryRow(`
SELECT g.token_hash, g.kind, g.session_id, g.turn_id, g.repo, g.can_push, g.expires_at, IFNULL(t.state,'')
FROM credential_grants g
LEFT JOIN turns t ON t.id=g.turn_id
WHERE g.token_hash=?`, tokenHash).Scan(&g.TokenHash, &g.Kind, &g.SessionID, &g.TurnID, &g.Repo, &push, &exp, &turnState)
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
	g.CanPush = push != 0
	if !now.Before(t) {
		return &g, false, nil
	}
	if g.Kind == GrantTurn && turnState != "leased" {
		return &g, false, nil
	}
	return &g, true, nil
}

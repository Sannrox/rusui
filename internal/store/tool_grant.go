package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

const ToolWrite = "write"

var (
	ErrUnknownToolGrant = errors.New("unknown tool grant")
	ErrToolGrantExists  = errors.New("tool grant already exists")
	ErrInvalidTool      = errors.New("invalid tool")
	ErrUserModeTools    = errors.New("user-mode sessions do not use space tool grants")
)

func NormalizeTool(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func ValidTool(name string) bool {
	switch NormalizeTool(name) {
	case ToolWrite, "execute", "edit", "delete", "move", "other":
		return true
	}
	return false
}

type SessionToolGrant struct {
	ID        int64      `json:"id"`
	SessionID int64      `json:"session_id"`
	Tool      string     `json:"tool"`
	CreatedBy string     `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

func GrantSessionToolTx(tx *sql.Tx, sessionID int64, tool, createdBy string) (SessionToolGrant, error) {
	tool = NormalizeTool(tool)
	if !ValidTool(tool) {
		return SessionToolGrant{}, ErrInvalidTool
	}
	createdBy = strings.TrimSpace(createdBy)
	if createdBy == "" {
		createdBy = DefaultTenantID
	}
	now := time.Now().UTC()
	res, err := tx.Exec(`INSERT INTO session_tool_grants (session_id, tool, created_by, created_at) VALUES (?,?,?,?)`,
		sessionID, tool, createdBy, now.Format(time.RFC3339Nano))
	if err != nil {
		if strings.Contains(strings.ToUpper(err.Error()), "UNIQUE") {
			return SessionToolGrant{}, ErrToolGrantExists
		}
		return SessionToolGrant{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return SessionToolGrant{}, err
	}
	return SessionToolGrant{ID: id, SessionID: sessionID, Tool: tool, CreatedBy: createdBy, CreatedAt: now}, nil
}

func GrantSessionTool(s *Store, sessionID int64, tool, createdBy string) (SessionToolGrant, error) {
	sess, err := GetSession(s, sessionID)
	if err != nil {
		return SessionToolGrant{}, err
	}
	if NormalizeExecutionMode(sess.ExecutionMode) == ExecutionUser {
		return SessionToolGrant{}, ErrUserModeTools
	}
	var g SessionToolGrant
	err = s.Tx(func(tx *sql.Tx) error {
		var inner error
		g, inner = GrantSessionToolTx(tx, sessionID, tool, createdBy)
		return inner
	})
	return g, err
}

func RevokeSessionToolTx(tx *sql.Tx, sessionID int64, tool string) (SessionToolGrant, error) {
	tool = NormalizeTool(tool)
	var g SessionToolGrant
	var created string
	err := tx.QueryRow(`SELECT id, session_id, tool, created_by, created_at FROM session_tool_grants WHERE session_id=? AND tool=? AND revoked_at IS NULL`,
		sessionID, tool).Scan(&g.ID, &g.SessionID, &g.Tool, &g.CreatedBy, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionToolGrant{}, ErrUnknownToolGrant
	}
	if err != nil {
		return SessionToolGrant{}, err
	}
	if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
		g.CreatedAt = t
	}
	now := time.Now().UTC()
	res, err := tx.Exec(`UPDATE session_tool_grants SET revoked_at=? WHERE id=? AND revoked_at IS NULL`,
		now.Format(time.RFC3339Nano), g.ID)
	if err != nil {
		return SessionToolGrant{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return SessionToolGrant{}, err
	}
	if n != 1 {
		return SessionToolGrant{}, ErrUnknownToolGrant
	}
	g.RevokedAt = &now
	return g, nil
}

func RevokeSessionTool(s *Store, sessionID int64, tool string) (SessionToolGrant, error) {
	var g SessionToolGrant
	err := s.Tx(func(tx *sql.Tx) error {
		var inner error
		g, inner = RevokeSessionToolTx(tx, sessionID, tool)
		return inner
	})
	return g, err
}

func ListSessionToolGrants(s *Store, sessionID int64) ([]SessionToolGrant, error) {
	rows, err := s.DB.Query(`SELECT id, session_id, tool, created_by, created_at FROM session_tool_grants WHERE session_id=? AND revoked_at IS NULL ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []SessionToolGrant{}
	for rows.Next() {
		var g SessionToolGrant
		var created string
		if err := rows.Scan(&g.ID, &g.SessionID, &g.Tool, &g.CreatedBy, &created); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
			g.CreatedAt = t
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func SessionToolGrantNames(s *Store, sessionID int64) ([]string, error) {
	list, err := ListSessionToolGrants(s, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(list))
	for _, g := range list {
		out = append(out, g.Tool)
	}
	return out, nil
}

func SessionHasToolGrant(s *Store, sessionID int64, tool string) (bool, error) {
	tool = NormalizeTool(tool)
	var one int
	err := s.DB.QueryRow(`SELECT 1 FROM session_tool_grants WHERE session_id=? AND revoked_at IS NULL AND (tool=? OR tool=?)`,
		sessionID, tool, ToolWrite).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func SpaceToolSet(mode string, grants []string) []string {
	if NormalizeExecutionMode(mode) == ExecutionUser {
		return []string{"user"}
	}
	out := []string{"read", "search", "fetch", "think"}
	seen := map[string]bool{"read": true, "search": true, "fetch": true, "think": true}
	for _, g := range grants {
		g = NormalizeTool(g)
		if g == "" || seen[g] {
			continue
		}
		seen[g] = true
		out = append(out, g)
	}
	return out
}

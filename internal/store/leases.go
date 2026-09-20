package store

import (
	"database/sql"
	"fmt"
	"strings"
)

type LiveTurn struct {
	TurnID    int64  `json:"turn_id"`
	SessionID int64  `json:"session_id"`
	Repo      string `json:"repo"`
	Project   string `json:"project"`
	Lane      string `json:"lane"`
	State     string `json:"state"`
}

func ListLeasedTurns(s *Store) ([]LiveTurn, error) {
	rows, err := s.DB.Query(`SELECT t.id, t.session_id, s.repo, s.project, t.lane, t.state
FROM turns t JOIN sessions s ON s.id=t.session_id
WHERE t.state='leased' ORDER BY t.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []LiveTurn
	for rows.Next() {
		var t LiveTurn
		if err := rows.Scan(&t.TurnID, &t.SessionID, &t.Repo, &t.Project, &t.Lane, &t.State); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func CountLeasedTurnsTx(tx *sql.Tx, project string, repos []string) (int, error) {
	args := []any{project}
	var b strings.Builder
	b.WriteString(`SELECT COUNT(*) FROM turns t JOIN sessions s ON s.id=t.session_id WHERE t.state='leased' AND (s.project=?`)
	if len(repos) > 0 {
		b.WriteString(` OR s.repo IN (`)
		for i, r := range repos {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString("?")
			args = append(args, r)
		}
		b.WriteString(`)`)
	}
	b.WriteString(`)`)
	var n int
	err := tx.QueryRow(b.String(), args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("leased turns: %w", err)
	}
	return n, nil
}

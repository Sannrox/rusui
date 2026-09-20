package store

import (
	"database/sql"
	"fmt"
	"strings"
)

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

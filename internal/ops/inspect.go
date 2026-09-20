package ops

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/sannrox/rusui/internal/store"
	_ "modernc.org/sqlite"
)

// InspectLive reads schema, global pause, and leased turns from a SQLite
// file without running migrations or taking the plane's write lock.
func InspectLive(path string) (schema int, paused bool, live []store.LiveTurn, err error) {
	if path == "" {
		return 0, false, nil, fmt.Errorf("diagnostics: db path required")
	}
	db, err := sql.Open("sqlite", path+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(1000)")
	if err != nil {
		return 0, false, nil, err
	}
	defer func() { _ = db.Close() }()
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&schema); err != nil {
		return 0, false, nil, fmt.Errorf("diagnostics: schema: %w", err)
	}
	var pause string
	err = db.QueryRow(`SELECT value FROM overlay WHERE key='pause:global'`).Scan(&pause)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil, fmt.Errorf("diagnostics: pause: %w", err)
	}
	paused = pause == "1"
	rows, err := db.Query(`SELECT t.id, t.session_id, s.repo, s.project, t.lane, t.state
FROM turns t JOIN sessions s ON s.id=t.session_id
WHERE t.state='leased' ORDER BY t.id`)
	if err != nil {
		return 0, false, nil, fmt.Errorf("diagnostics: live turns: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var t store.LiveTurn
		if err := rows.Scan(&t.TurnID, &t.SessionID, &t.Repo, &t.Project, &t.Lane, &t.State); err != nil {
			return 0, false, nil, err
		}
		live = append(live, t)
	}
	if live == nil {
		live = []store.LiveTurn{}
	}
	return schema, paused, live, rows.Err()
}

package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
)

// OpenReadOnly opens an existing SQLite file read-only without running
// migrations, so offline tools (policy simulate/explain) can read runtime
// state without creating or upgrading the database.
func OpenReadOnly(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	fileURL := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	db, err := sql.Open("sqlite", fileURL.String()+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{DB: db, Dir: filepath.Dir(abs)}, nil
}

// InspectLive reads schema, global pause, and leased turns from a SQLite
// file without running migrations or taking the plane's write lock.
func InspectLive(path string) (schema int, paused bool, live []LiveTurn, err error) {
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
		var t LiveTurn
		if err := rows.Scan(&t.TurnID, &t.SessionID, &t.Repo, &t.Project, &t.Lane, &t.State); err != nil {
			return 0, false, nil, err
		}
		live = append(live, t)
	}
	if live == nil {
		live = []LiveTurn{}
	}
	return schema, paused, live, rows.Err()
}

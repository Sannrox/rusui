package store

import (
	"database/sql"
	"strings"
	"time"
)

// EnvironmentCapture is the stored output of the last setup, resume, or
// declared-service start of an environment (#334). Output is UTF-8 with
// invalid bytes replaced; it is text, never markup.
type EnvironmentCapture struct {
	Kind       string    `json:"kind"`
	Name       string    `json:"name,omitempty"`
	Output     string    `json:"output"`
	Truncated  bool      `json:"truncated"`
	Failed     bool      `json:"failed"`
	RecordedAt time.Time `json:"recorded_at"`
}

// CaptureRow is one capture to store. Output is raw bytes.
type CaptureRow struct {
	Name      string
	Output    []byte
	Truncated bool
	Failed    bool
}

// ReplaceEnvironmentCaptures replaces every capture of kind for the
// environment with rows. An empty rows clears the kind, so a hook that did
// not run leaves no earlier body behind.
func ReplaceEnvironmentCaptures(s *Store, envID int64, kind string, rows []CaptureRow, now time.Time) error {
	at := now.UTC().Format(time.RFC3339Nano)
	return s.Tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM environment_captures WHERE environment_id=? AND kind=?`, envID, kind); err != nil {
			return err
		}
		for _, r := range rows {
			out := r.Output
			if out == nil {
				out = []byte{}
			}
			if _, err := tx.Exec(`INSERT OR REPLACE INTO environment_captures
(environment_id, kind, name, output, truncated, failed, recorded_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				envID, kind, r.Name, out, r.Truncated, r.Failed, at); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListSessionCaptures returns the captures of the session's environment in
// setup, resume, service order.
func ListSessionCaptures(s *Store, sessionID int64) ([]EnvironmentCapture, error) {
	rows, err := s.DB.Query(`SELECT c.kind, c.name, c.output, c.truncated, c.failed, c.recorded_at
FROM environment_captures c JOIN sessions se ON se.environment_id = c.environment_id
WHERE se.id=?
ORDER BY CASE c.kind WHEN 'setup' THEN 0 WHEN 'resume' THEN 1 ELSE 2 END, c.name`, sessionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []EnvironmentCapture{}
	for rows.Next() {
		var c EnvironmentCapture
		var raw []byte
		var at string
		if err := rows.Scan(&c.Kind, &c.Name, &raw, &c.Truncated, &c.Failed, &at); err != nil {
			return nil, err
		}
		c.Output = strings.ToValidUTF8(string(raw), "�")
		if c.RecordedAt, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

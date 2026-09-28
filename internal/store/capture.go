package store

import (
	"crypto/sha256"
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

// CaptureListFilter selects stored captures. Empty Kind and Name mean
// every row. OmitBody leaves Output empty so the BLOB is not loaded.
type CaptureListFilter struct {
	Kind     string
	Name     string
	OmitBody bool
}

type storedCapture struct {
	sum       [32]byte
	truncated bool
	failed    bool
}

func captureSum(out []byte) [32]byte {
	if out == nil {
		out = []byte{}
	}
	return sha256.Sum256(out)
}

func loadKindCaptures(tx *sql.Tx, envID int64, kind string) (map[string]storedCapture, error) {
	rows, err := tx.Query(`SELECT name, output, truncated, failed FROM environment_captures WHERE environment_id=? AND kind=?`, envID, kind)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]storedCapture{}
	for rows.Next() {
		var name string
		var raw []byte
		var c storedCapture
		if err := rows.Scan(&name, &raw, &c.truncated, &c.failed); err != nil {
			return nil, err
		}
		c.sum = captureSum(raw)
		out[name] = c
	}
	return out, rows.Err()
}

// ReplaceEnvironmentCaptures replaces every capture of kind for the
// environment with rows. An empty rows clears the kind, so a hook that did
// not run leaves no earlier body behind. A row whose sha256 matches the
// stored body is left in place; only new, removed, or changed rows write.
func ReplaceEnvironmentCaptures(s *Store, envID int64, kind string, rows []CaptureRow, now time.Time) error {
	at := now.UTC().Format(time.RFC3339Nano)
	return s.Tx(func(tx *sql.Tx) error {
		if len(rows) == 0 {
			_, err := tx.Exec(`DELETE FROM environment_captures WHERE environment_id=? AND kind=?`, envID, kind)
			return err
		}
		existing, err := loadKindCaptures(tx, envID, kind)
		if err != nil {
			return err
		}
		keep := make(map[string]struct{}, len(rows))
		for _, r := range rows {
			out := r.Output
			if out == nil {
				out = []byte{}
			}
			keep[r.Name] = struct{}{}
			old, ok := existing[r.Name]
			if ok && old.sum == captureSum(out) {
				if old.truncated == r.Truncated && old.failed == r.Failed {
					continue
				}
				if _, err := tx.Exec(`UPDATE environment_captures SET truncated=?, failed=?, recorded_at=? WHERE environment_id=? AND kind=? AND name=?`,
					r.Truncated, r.Failed, at, envID, kind, r.Name); err != nil {
					return err
				}
				continue
			}
			if _, err := tx.Exec(`INSERT OR REPLACE INTO environment_captures
(environment_id, kind, name, output, truncated, failed, recorded_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				envID, kind, r.Name, out, r.Truncated, r.Failed, at); err != nil {
				return err
			}
		}
		for name := range existing {
			if _, ok := keep[name]; ok {
				continue
			}
			if _, err := tx.Exec(`DELETE FROM environment_captures WHERE environment_id=? AND kind=? AND name=?`, envID, kind, name); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListSessionCaptures returns the captures of the session's environment in
// setup, resume, service order. Filter Kind and Name narrow the rows;
// OmitBody skips the output BLOB.
func ListSessionCaptures(s *Store, sessionID int64, filter CaptureListFilter) ([]EnvironmentCapture, error) {
	q := `SELECT c.kind, c.name, c.output, c.truncated, c.failed, c.recorded_at
FROM environment_captures c JOIN sessions se ON se.environment_id = c.environment_id
WHERE se.id=?`
	args := []any{sessionID}
	if filter.OmitBody {
		q = `SELECT c.kind, c.name, x'', c.truncated, c.failed, c.recorded_at
FROM environment_captures c JOIN sessions se ON se.environment_id = c.environment_id
WHERE se.id=?`
	}
	if filter.Kind != "" {
		q += ` AND c.kind=?`
		args = append(args, filter.Kind)
	}
	if filter.Name != "" {
		q += ` AND c.name=?`
		args = append(args, filter.Name)
	}
	q += ` ORDER BY CASE c.kind WHEN 'setup' THEN 0 WHEN 'resume' THEN 1 ELSE 2 END, c.name`
	rows, err := s.DB.Query(q, args...)
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
		if !filter.OmitBody {
			c.Output = strings.ToValidUTF8(string(raw), "�")
		}
		if c.RecordedAt, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

package store

import (
	"database/sql"
	"errors"
	"time"
)

var ErrEnvironmentUnavailable = errors.New("environment is not ready")
var ErrEnvironmentBusy = errors.New("environment is not eligible for sleep")

func ListIdleContainerEnvironments(s *Store, now time.Time, ttl, idle time.Duration) ([]Environment, error) {
	if idle <= 0 || ttl <= idle {
		return nil, nil
	}
	cutoff := now.Add(ttl - idle).UTC().Format(time.RFC3339Nano)
	rows, err := s.DB.Query(`SELECT id, name, driver, state, handle, source_hash, expires_at, slept_at, cpu_millis, memory_bytes, created_at
FROM environments WHERE name!=? AND driver='container' AND state=? AND expires_at>? AND expires_at<=? ORDER BY id`,
		LocalEnvironmentName, EnvReady, now.UTC().Format(time.RFC3339Nano), cutoff)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Environment
	for rows.Next() {
		e, err := scanEnvironment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// ReserveEnvironmentSleep makes the eligibility check and sleeping state
// visible atomically, so new grants cannot race with stopping the runtime.
func ReserveEnvironmentSleep(s *Store, id int64, now time.Time) (bool, error) {
	return reserveEnvironmentSleep(s, id, now, 0, 0)
}

func ReserveIdleEnvironmentSleep(s *Store, id int64, now time.Time, ttl, idle time.Duration) (bool, error) {
	if idle <= 0 || ttl <= idle {
		return false, nil
	}
	return reserveEnvironmentSleep(s, id, now, ttl, idle)
}

func reserveEnvironmentSleep(s *Store, id int64, now time.Time, ttl, idle time.Duration) (bool, error) {
	reserved := false
	err := s.Tx(func(tx *sql.Tx) error {
		var state, driver, name string
		var expiresAt sql.NullString
		if err := tx.QueryRow(`SELECT state, driver, name, expires_at FROM environments WHERE id=?`, id).Scan(&state, &driver, &name, &expiresAt); err != nil {
			return err
		}
		if state != EnvReady || driver == "" || name == LocalEnvironmentName {
			return nil
		}
		if idle > 0 {
			if !expiresAt.Valid {
				return nil
			}
			lastActivityExpiry, err := time.Parse(time.RFC3339Nano, expiresAt.String)
			if err != nil {
				return err
			}
			if !now.Before(lastActivityExpiry) || lastActivityExpiry.After(now.Add(ttl-idle)) {
				return nil
			}
		}
		nowText := now.UTC().Format(time.RFC3339Nano)
		var active int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM turns t JOIN sessions s ON s.id=t.session_id
WHERE s.environment_id=? AND t.state IN ('queued','leased')`, id).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			return nil
		}
		if err := tx.QueryRow(`SELECT COUNT(*) FROM terminal_leases WHERE environment_id=? AND expires_at>?`, id, nowText).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			return nil
		}
		if err := tx.QueryRow(`SELECT COUNT(*) FROM preview_grants WHERE environment_id=? AND revoked=0 AND expires_at>?`, id, nowText).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			return nil
		}
		res, err := tx.Exec(`UPDATE environments SET state=?, slept_at=? WHERE id=? AND state=?`,
			EnvSleeping, nowText, id, EnvReady)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		reserved = n == 1
		return err
	})
	return reserved, err
}

func RestoreEnvironmentReady(s *Store, id int64) error {
	_, err := s.DB.Exec(`UPDATE environments SET state=?, slept_at=NULL WHERE id=? AND state=?`, EnvReady, id, EnvSleeping)
	return err
}

func InsertEnvironmentReceipt(s *Store, envID int64, kind, state, detail string, now time.Time) error {
	_, err := s.DB.Exec(`INSERT INTO environment_receipts (environment_id, session_id, kind, state, detail, created_at)
VALUES (?, (SELECT id FROM sessions WHERE environment_id=? ORDER BY id LIMIT 1), ?, ?, ?, ?)`,
		envID, envID, kind, state, detail, now.UTC().Format(time.RFC3339Nano))
	return err
}

func ListEnvironmentReceipts(s *Store, sessionID int64) ([]EnvironmentReceipt, error) {
	rows, err := s.DB.Query(`SELECT id, environment_id, session_id, kind, state, detail, created_at
FROM environment_receipts WHERE session_id=? ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []EnvironmentReceipt
	for rows.Next() {
		var receipt EnvironmentReceipt
		var sid sql.NullInt64
		var created string
		if err := rows.Scan(&receipt.ID, &receipt.EnvironmentID, &sid, &receipt.Kind, &receipt.State, &receipt.Detail, &created); err != nil {
			return nil, err
		}
		if sid.Valid {
			receipt.SessionID = &sid.Int64
		}
		t, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}
		receipt.CreatedAt = t
		out = append(out, receipt)
	}
	return out, rows.Err()
}

func TouchSessionEnvironmentTx(tx *sql.Tx, sessionID int64, now, expiresAt time.Time) error {
	var environmentID int64
	var currentExpiry sql.NullString
	err := tx.QueryRow(`SELECT id, expires_at FROM environments
WHERE id=(SELECT environment_id FROM sessions WHERE id=?) AND state!=?`, sessionID, EnvExpired).
		Scan(&environmentID, &currentExpiry)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if currentExpiry.Valid {
		current, err := time.Parse(time.RFC3339Nano, currentExpiry.String)
		if err != nil {
			return err
		}
		if !current.After(now) {
			return nil
		}
	}
	query := `UPDATE environments SET expires_at=? WHERE id=? AND state!=? AND expires_at IS NULL`
	args := []any{expiresAt.UTC().Format(time.RFC3339Nano), environmentID, EnvExpired}
	if currentExpiry.Valid {
		query = `UPDATE environments SET expires_at=? WHERE id=? AND state!=? AND expires_at=?`
		args = append(args, currentExpiry.String)
	}
	_, err = tx.Exec(query, args...)
	return err
}

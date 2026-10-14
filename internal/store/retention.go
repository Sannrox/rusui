package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

const (
	RetentionShort      = "short"
	RetentionIndefinite = "indefinite"
)

var (
	ErrInvalidRetention       = errors.New("invalid retention")
	ErrRetentionNotIndefinite = errors.New("write override requires indefinite retention")
	ErrWriteOverrideExists    = errors.New("write override already recorded")
	ErrRetentionExpired       = errors.New("session retention already expired")
)

func NormalizeRetention(class string) string {
	switch strings.ToLower(strings.TrimSpace(class)) {
	case "", RetentionShort:
		return RetentionShort
	case RetentionIndefinite:
		return RetentionIndefinite
	default:
		return strings.ToLower(strings.TrimSpace(class))
	}
}

func ValidRetention(class string) bool {
	switch NormalizeRetention(class) {
	case RetentionShort, RetentionIndefinite:
		return true
	}
	return false
}

func DefaultRetentionExpiry(now time.Time) time.Time {
	return now.UTC().Add(EnvTTL)
}

func ensureSessionRetentionTx(tx *sql.Tx, sessionID int64, now time.Time) error {
	exp := DefaultRetentionExpiry(now).Format(time.RFC3339Nano)
	_, err := tx.Exec(`INSERT OR IGNORE INTO session_retention (session_id, class, expires_at) VALUES (?, ?, ?)`,
		sessionID, RetentionShort, exp)
	return err
}

func SetSessionRetentionTx(tx *sql.Tx, sessionID int64, class string, expiresAt *time.Time) error {
	class = NormalizeRetention(class)
	if !ValidRetention(class) {
		return ErrInvalidRetention
	}
	var exp any
	if class == RetentionShort {
		if expiresAt == nil {
			t := DefaultRetentionExpiry(time.Now())
			expiresAt = &t
		}
		exp = expiresAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := tx.Exec(`
INSERT INTO session_retention (session_id, class, expires_at, write_override, write_override_by, write_override_at, expired)
VALUES (?, ?, ?, 0, '', NULL, 0)
ON CONFLICT(session_id) DO UPDATE SET
  class=excluded.class,
  expires_at=excluded.expires_at,
  expired=0`, sessionID, class, exp)
	return err
}

func SetRetentionWriteOverrideTx(tx *sql.Tx, sessionID int64, actor string, now time.Time) error {
	var class string
	var override int
	err := tx.QueryRow(`SELECT class, write_override FROM session_retention WHERE session_id=?`, sessionID).Scan(&class, &override)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRetentionNotIndefinite
	}
	if err != nil {
		return err
	}
	if NormalizeRetention(class) != RetentionIndefinite {
		return ErrRetentionNotIndefinite
	}
	if override != 0 {
		return ErrWriteOverrideExists
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = DefaultTenantID
	}
	_, err = tx.Exec(`UPDATE session_retention SET write_override=1, write_override_by=?, write_override_at=? WHERE session_id=?`,
		actor, now.UTC().Format(time.RFC3339Nano), sessionID)
	return err
}

func ClearSessionDataTx(tx *sql.Tx, sessionID int64) error {
	if _, err := tx.Exec(`UPDATE sessions SET prompt='', guest_config='', guest_pin='', guest_session_id='' WHERE id=?`, sessionID); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM prompt_attachments WHERE session_id=?`, sessionID)
	return err
}

func MarkRetentionExpiredTx(tx *sql.Tx, sessionID int64) error {
	_, err := tx.Exec(`UPDATE session_retention SET expired=1 WHERE session_id=?`, sessionID)
	return err
}

func ListDueRetentionSessionIDs(s *Store, now time.Time) ([]int64, error) {
	rows, err := s.DB.Query(`SELECT session_id, expires_at FROM session_retention WHERE class=? AND expired=0 AND expires_at IS NOT NULL`, RetentionShort)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		var expires string
		if err := rows.Scan(&id, &expires); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339Nano, expires)
		if err != nil {
			continue
		}
		if !t.After(now.UTC()) {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

func fillSessionRetention(q sessionRow, sess *Session) error {
	var class string
	var expires sql.NullString
	var override, expired int
	err := q.QueryRow(`SELECT class, expires_at, write_override, expired FROM session_retention WHERE session_id=?`, sess.ID).
		Scan(&class, &expires, &override, &expired)
	if errors.Is(err, sql.ErrNoRows) {
		sess.Retention = RetentionShort
		return nil
	}
	if err != nil {
		return err
	}
	sess.Retention = NormalizeRetention(class)
	sess.WriteOverride = override != 0
	sess.RetentionExpired = expired != 0
	if expires.Valid && expires.String != "" {
		t, err := time.Parse(time.RFC3339Nano, expires.String)
		if err != nil {
			return err
		}
		sess.RetentionExpiresAt = &t
	}
	return nil
}

func fillListRetention(s *Store, list []Session) error {
	if len(list) == 0 {
		return nil
	}
	idx := make(map[int64]int, len(list))
	ids := make([]any, 0, len(list))
	for i := range list {
		list[i].Retention = RetentionShort
		idx[list[i].ID] = i
		ids = append(ids, list[i].ID)
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	rows, err := s.DB.Query(`SELECT session_id, class, expires_at, write_override, expired FROM session_retention WHERE session_id IN (`+placeholders+`)`, ids...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var class string
		var expires sql.NullString
		var override, expired int
		if err := rows.Scan(&id, &class, &expires, &override, &expired); err != nil {
			return err
		}
		i, ok := idx[id]
		if !ok {
			continue
		}
		list[i].Retention = NormalizeRetention(class)
		list[i].WriteOverride = override != 0
		list[i].RetentionExpired = expired != 0
		if expires.Valid && expires.String != "" {
			t, err := time.Parse(time.RFC3339Nano, expires.String)
			if err != nil {
				return err
			}
			list[i].RetentionExpiresAt = &t
		}
	}
	return rows.Err()
}

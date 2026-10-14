package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/sannrox/rusui/internal/snapshot"
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
	if err := refuseExpiredRetentionTx(tx, sessionID); err != nil {
		return err
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
  write_override=CASE WHEN session_retention.class=excluded.class THEN session_retention.write_override ELSE 0 END,
  write_override_by=CASE WHEN session_retention.class=excluded.class THEN session_retention.write_override_by ELSE '' END,
  write_override_at=CASE WHEN session_retention.class=excluded.class THEN session_retention.write_override_at ELSE NULL END,
  expired=0`, sessionID, class, exp)
	return err
}

func refuseExpiredRetentionTx(tx *sql.Tx, sessionID int64) error {
	var expired int
	err := tx.QueryRow(`SELECT expired FROM session_retention WHERE session_id=?`, sessionID).Scan(&expired)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if expired != 0 {
		return ErrRetentionExpired
	}
	return nil
}

func SetRetentionWriteOverrideTx(tx *sql.Tx, sessionID int64, actor string, now time.Time) error {
	if err := refuseExpiredRetentionTx(tx, sessionID); err != nil {
		return err
	}
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

func ClearSessionDataTx(s *Store, tx *sql.Tx, sessionID int64) error {
	if _, err := tx.Exec(`UPDATE sessions SET prompt='', guest_config='', guest_pin='', guest_session_id='' WHERE id=?`, sessionID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE followup_queue SET prompt='' WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE turn_steers SET prompt='' WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tasks SET prompt='' WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	if err := scrubSteerActionPromptsTx(tx, sessionID); err != nil {
		return err
	}
	if err := scrubSessionSnapshotBodiesTx(tx, sessionID); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT DISTINCT digest FROM prompt_attachments WHERE session_id=? AND digest != ''`, sessionID)
	if err != nil {
		return err
	}
	var digests []string
	for rows.Next() {
		var digest string
		if err := rows.Scan(&digest); err != nil {
			_ = rows.Close()
			return err
		}
		digests = append(digests, digest)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM prompt_attachments WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	for _, digest := range digests {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM prompt_attachments WHERE digest=?`, digest).Scan(&n); err != nil {
			return err
		}
		if n != 0 || s == nil {
			continue
		}
		_ = os.Remove(s.AttachmentFile(digest))
	}
	return nil
}

func scrubSteerActionPromptsTx(tx *sql.Tx, sessionID int64) error {
	rows, err := tx.Query(`SELECT action_id, body FROM actions WHERE session_id=? AND action_type=?`, sessionID, "operator.steer")
	if err != nil {
		return err
	}
	type row struct {
		id   string
		body string
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.body); err != nil {
			_ = rows.Close()
			return err
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, r := range list {
		var m map[string]any
		if err := json.Unmarshal([]byte(r.body), &m); err != nil {
			m = map[string]any{}
		}
		m["prompt"] = ""
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE actions SET body=? WHERE action_id=?`, string(b), r.id); err != nil {
			return err
		}
	}
	return nil
}

func scrubSessionSnapshotBodiesTx(tx *sql.Tx, sessionID int64) error {
	var kind, repo string
	var item int
	if err := tx.QueryRow(`SELECT kind, repo, item FROM sessions WHERE id=?`, sessionID).Scan(&kind, &repo, &item); err != nil {
		return err
	}
	switch kind {
	case SessionKindRun, SessionKindScheduled, SessionKindLocal:
	default:
		return nil
	}
	rows, err := tx.Query(`SELECT revision, payload FROM snapshots WHERE repo=? AND item=?`, repo, item)
	if err != nil {
		return err
	}
	type row struct {
		rev     int
		payload string
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.rev, &r.payload); err != nil {
			_ = rows.Close()
			return err
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, r := range list {
		var it snapshot.Item
		if err := json.Unmarshal([]byte(r.payload), &it); err != nil {
			continue
		}
		it.Body = ""
		b, err := json.Marshal(it)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE snapshots SET payload=? WHERE repo=? AND item=? AND revision=?`, string(b), repo, item, r.rev); err != nil {
			return err
		}
	}
	return nil
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

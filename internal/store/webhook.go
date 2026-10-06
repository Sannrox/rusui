package store

import (
	"database/sql"
	"time"
)

func GetSessionWebhook(s *Store, sessionID int64) (string, bool, error) {
	var secret string
	err := s.DB.QueryRow(`SELECT secret FROM session_webhooks WHERE session_id=?`, sessionID).Scan(&secret)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return secret, true, nil
}

func PutSessionWebhook(s *Store, sessionID int64, secret string, now time.Time) error {
	_, err := s.DB.Exec(`INSERT OR IGNORE INTO session_webhooks(session_id, secret, created_at) VALUES(?,?,?)`,
		sessionID, secret, now.UTC().Format(time.RFC3339Nano))
	return err
}

func InsertWebhookDelivery(s *Store, sessionID int64, deliveryID string, accepted bool, detail string, now time.Time) (bool, error) {
	flag := 0
	if accepted {
		flag = 1
	}
	res, err := s.DB.Exec(`INSERT OR IGNORE INTO session_webhook_deliveries(session_id, delivery_id, accepted, detail, created_at) VALUES(?,?,?,?,?)`,
		sessionID, deliveryID, flag, detail, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

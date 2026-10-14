package engine

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

func (e *Engine) applyDefaultRetentionTx(tx *sql.Tx, sessionID int64) error {
	exp := store.DefaultRetentionExpiry(e.now())
	return store.SetSessionRetentionTx(tx, sessionID, store.RetentionShort, &exp)
}

// ExpireRetainedSessions archives short-lived sessions whose TTL has
// elapsed, removes session data, and records an audit action (#609).
func (e *Engine) ExpireRetainedSessions() error {
	ids, err := store.ListDueRetentionSessionIDs(e.Store, e.now())
	if err != nil {
		return err
	}
	var joined error
	for _, id := range ids {
		if err := e.ExpireSession(id); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func (e *Engine) ExpireSession(sessionID int64) error {
	return e.finishRetention(sessionID, "retention.expire", "Short-lived session data was removed after the retention TTL.", true)
}

func (e *Engine) DeleteSessionRetention(sessionID int64) error {
	return e.finishRetention(sessionID, "retention.delete", "Session data was removed by an explicit retention deletion.", false)
}

func dueForExpiry(sess *store.Session, now time.Time) bool {
	if sess == nil || sess.RetentionExpired {
		return false
	}
	if sess.Retention != store.RetentionShort {
		return false
	}
	if sess.RetentionExpiresAt == nil || sess.RetentionExpiresAt.After(now) {
		return false
	}
	return true
}

func (e *Engine) finishRetention(sessionID int64, actionType, sentence string, requireDue bool) error {
	err := e.Store.Tx(func(tx *sql.Tx) error {
		sess, err := store.GetSessionTx(tx, sessionID)
		if err != nil {
			return err
		}
		if sess.RetentionExpired {
			return nil
		}
		if requireDue && !dueForExpiry(sess, e.now()) {
			return nil
		}
		if err := store.ClearSessionDataTx(e.Store, tx, sessionID); err != nil {
			return err
		}
		if err := store.MarkRetentionExpiredTx(tx, sessionID); err != nil {
			return err
		}
		body, err := json.Marshal(map[string]any{"retention": sess.Retention})
		if err != nil {
			return err
		}
		return store.InsertActionTx(tx, store.Action{
			ID: fmt.Sprintf("%s-%d-%d", actionType, sessionID, e.now().UnixNano()), SessionID: &sessionID,
			Type: actionType, ReasonCode: "recorded", EvidenceClass: "plane_observed",
			LimitSentence: sentence, Body: string(body),
		})
	})
	if err != nil {
		return err
	}
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return err
	}
	if !sess.RetentionExpired {
		return nil
	}
	return e.ArchiveSession(sessionID)
}

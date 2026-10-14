package engine

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

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
	return e.finishRetention(sessionID, "retention.expire", "Short-lived session data was removed after the retention TTL.")
}

func (e *Engine) DeleteSessionRetention(sessionID int64) error {
	return e.finishRetention(sessionID, "retention.delete", "Session data was removed by an explicit retention deletion.")
}

func (e *Engine) finishRetention(sessionID int64, actionType, sentence string) error {
	if err := e.ArchiveSession(sessionID); err != nil {
		return err
	}
	return e.Store.Tx(func(tx *sql.Tx) error {
		sess, err := store.GetSessionTx(tx, sessionID)
		if err != nil {
			return err
		}
		if sess.RetentionExpired {
			return nil
		}
		if err := store.ClearSessionDataTx(tx, sessionID); err != nil {
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
}

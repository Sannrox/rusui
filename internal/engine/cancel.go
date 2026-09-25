package engine

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/sannrox/rusui/internal/store"
)

func (e *Engine) CancelSession(sessionID int64) error {
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return err
	}
	if sess.Kind == store.SessionKindLocal {
		_, err := e.CancelLocalSession(sessionID)
		return err
	}
	turns, err := store.ListTurnsForSession(e.Store, sessionID)
	if err != nil {
		return err
	}
	if len(turns) == 0 {
		return fmt.Errorf("no turns")
	}
	err = e.Store.Tx(func(tx *sql.Tx) error {
		j, err := store.GetJobByIDTx(tx, turns[0].ID)
		if err != nil {
			return err
		}
		if j.State == "leased" {
			receipt := map[string]any{"kind": "cancelled"}
			rb, _ := json.Marshal(receipt)
			if err := store.InsertReceiptTx(tx, j.ID, j.LeaseGeneration, j.ClaimedRevision, "cancelled", string(rb)); err != nil {
				return err
			}
			if j.ClaimedRevision < j.PendingRevision {
				j.State = "queued"
			} else {
				j.State = "failed"
			}
			if err := store.UpdateJobTx(tx, j); err != nil {
				return err
			}
			turn, err := store.GetTurnTx(tx, j.ID)
			if err != nil {
				return err
			}
			if err := store.TouchSessionEnvironmentTx(tx, turn.SessionID, e.now().Add(e.envTTL())); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if envRow, err := store.GetEnvironment(e.Store, sess.EnvironmentID); err == nil && envRow.Handle != "" {
		if k, ok := e.envDriver().(guestKiller); ok {
			_ = k.KillGuest(envRow.Handle)
		}
	}
	return nil
}

type guestKiller interface {
	KillGuest(handle string) error
}

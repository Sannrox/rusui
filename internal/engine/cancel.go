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
		for _, turn := range turns {
			if err := e.cancelTurnTx(tx, turn.ID); err != nil {
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

func (e *Engine) cancelTurnTx(tx *sql.Tx, turnID int64) error {
	j, err := store.GetJobByIDTx(tx, turnID)
	if err != nil {
		return err
	}
	// A turn that was never leased is still claimable. Cancel makes it
	// terminal without a lease receipt. A leased turn keeps the receipt
	// path, including requeue when a follow-up or steer is still owed.
	if j.State == "queued" {
		j.State = "failed"
		if err := store.UpdateJobTx(tx, j); err != nil {
			return err
		}
		return e.finishMeasurementTx(tx, j.ID, j.State, Artifact{}, e.now())
	}
	if j.State != "leased" {
		return nil
	}
	receipt := map[string]any{"kind": "cancelled"}
	rb, _ := json.Marshal(receipt)
	if err := store.InsertReceiptTx(tx, j.ID, j.LeaseGeneration, j.ClaimedRevision, "cancelled", string(rb)); err != nil {
		return err
	}
	steers, err := store.PromoteSteersTx(tx, j.ID, j.LeaseGeneration)
	if err != nil {
		return err
	}
	if j.ClaimedRevision < j.PendingRevision {
		j.State = "queued"
	} else if steers > 0 {
		j.State = "queued"
		j.RetryCount = 0
		if err := applyNextFollowUpTx(tx, j); err != nil {
			return err
		}
	} else {
		j.State = "failed"
	}
	if err := store.UpdateJobTx(tx, j); err != nil {
		return err
	}
	now := e.now()
	if j.State == "failed" {
		if err := e.finishMeasurementTx(tx, j.ID, j.State, Artifact{}, now); err != nil {
			return err
		}
	}
	turn, err := store.GetTurnTx(tx, j.ID)
	if err != nil {
		return err
	}
	return store.TouchSessionEnvironmentTx(tx, turn.SessionID, now, now.Add(e.envTTL()))
}

type guestKiller interface {
	KillGuest(handle string) error
}

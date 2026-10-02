package engine

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

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
	// The environment operation is held from before the cancel commits
	// until the kill and the service restart are done. Claim skips an
	// environment under an operation, so a turn the cancel requeues cannot
	// start a harness that the kill then hits (#446).
	envRow, envErr := store.GetEnvironment(e.Store, sess.EnvironmentID)
	if envErr == nil && envRow.Handle != "" {
		if release, ok := e.waitEnvironmentOperation(envRow.ID, cancelEnvWait); ok {
			defer release()
		} else {
			e.exception(fmt.Sprintf("cancel session=%d: environment %d stayed busy for %s; cancelling without holding it", sessionID, envRow.ID, cancelEnvWait))
		}
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
	if envErr != nil || envRow.Handle == "" {
		return nil
	}
	// The environment's own driver: a container session is killed by
	// the container driver, not the process driver (#441).
	d, err := e.driverFor(envRow.Driver)
	if err != nil {
		return nil
	}
	k, ok := d.(guestKiller)
	if !ok {
		return nil
	}
	if err := k.KillGuest(envRow.Handle); err != nil {
		e.exception(fmt.Sprintf("cancel session=%d: stop guest of environment %d: %v", sessionID, envRow.ID, err))
	}
	// The kill also ends the declared services; a follow-up turn on this
	// environment still expects them.
	if envRow.State == store.EnvReady {
		if err := e.startServicesFor(d, envRow.ID, envRow.Handle); err != nil {
			e.exception(fmt.Sprintf("cancel session=%d: restart services of environment %d: %v", sessionID, envRow.ID, err))
		}
	}
	return nil
}

// cancelEnvWait bounds how long a cancel waits for another environment
// operation, so a stuck one cannot hang the cancel request.
const cancelEnvWait = 30 * time.Second

// waitEnvironmentOperation takes the environment operation, waiting up to
// limit for one in progress (sleep, wake, or another cancel) to finish.
func (e *Engine) waitEnvironmentOperation(id int64, limit time.Duration) (func(), bool) {
	deadline := time.Now().Add(limit)
	for {
		if release, ok := e.beginEnvironmentOperation(id); ok {
			return release, true
		}
		if time.Now().After(deadline) {
			return nil, false
		}
		time.Sleep(20 * time.Millisecond)
	}
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

package engine

import (
	"database/sql"
	"errors"

	"github.com/sannrox/rusui/internal/store"
)

// ArchiveSession sleeps the session's environment, refuses new prompts, and
// stops work aimed at that session and its children. Session rows and
// environment ids stay (#499, ADR 0063).
func (e *Engine) ArchiveSession(sessionID int64) error {
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return err
	}
	if !sess.Archived {
		if err := store.SetSessionArchived(e.Store, sessionID, true, e.now()); err != nil {
			return err
		}
	}
	turns, err := store.ListTurnsForSession(e.Store, sessionID)
	if err != nil {
		return err
	}
	if len(turns) > 0 {
		if _, err := e.DropQueuedPrompts(sessionID); err != nil {
			return err
		}
		if err := e.failArchivedTurns(sessionID); err != nil {
			return err
		}
	}
	children, err := store.ListChildSessionIDs(e.Store, sessionID)
	if err != nil {
		return err
	}
	var childErr error
	for _, childID := range children {
		if err := e.ArchiveSession(childID); err != nil {
			childErr = errors.Join(childErr, err)
		}
	}
	envRow, err := store.GetEnvironment(e.Store, sess.EnvironmentID)
	if err != nil {
		return errors.Join(childErr, err)
	}
	if envRow.Name == store.LocalEnvironmentName || envRow.Handle == "" || envRow.State != store.EnvReady {
		return childErr
	}
	_ = store.DeleteTerminalLease(e.Store, envRow.ID)
	_ = store.RevokePreviewGrantsForEnvironment(e.Store, envRow.ID)
	_, err = e.SleepEnvironment(envRow.ID)
	if err == nil || errors.Is(err, store.ErrEnvironmentBusy) {
		return childErr
	}
	return errors.Join(childErr, err)
}

// UnarchiveSession allows prompts again on the same session and environment.
func (e *Engine) UnarchiveSession(sessionID int64) error {
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return err
	}
	if !sess.Archived {
		return nil
	}
	return store.SetSessionArchived(e.Store, sessionID, false, e.now())
}

func (e *Engine) failArchivedTurns(sessionID int64) error {
	turns, err := store.ListTurnsForSession(e.Store, sessionID)
	if err != nil {
		return err
	}
	if len(turns) == 0 {
		return nil
	}
	return e.Store.Tx(func(tx *sql.Tx) error {
		for _, turn := range turns {
			if err := e.cancelTurnTx(tx, turn.ID); err != nil {
				return err
			}
			j, err := store.GetJobByIDTx(tx, turn.ID)
			if err != nil {
				return err
			}
			if j.State != "queued" {
				continue
			}
			j.State = "failed"
			if err := store.UpdateJobTx(tx, j); err != nil {
				return err
			}
			if err := e.finishMeasurementTx(tx, j.ID, j.State, Artifact{}, e.now()); err != nil {
				return err
			}
		}
		return nil
	})
}

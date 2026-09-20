package engine

import (
	"database/sql"
	"fmt"

	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

func (e *Engine) PromptFollowUp(sessionID int64, prompt string) (int64, int, error) {
	if prompt == "" {
		return 0, 0, fmt.Errorf("prompt required")
	}
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return 0, 0, err
	}
	turns, err := store.ListTurnsForSession(e.Store, sessionID)
	if err != nil {
		return 0, 0, err
	}
	if len(turns) == 0 {
		return 0, 0, fmt.Errorf("no turns")
	}
	var turnID int64
	var pending int
	err = e.Store.Tx(func(tx *sql.Tx) error {
		paused, err := store.Paused(tx, sess.Project)
		if err != nil {
			return err
		}
		if paused {
			return errPaused
		}
		j, err := store.GetJobByIDTx(tx, turns[0].ID)
		if err != nil {
			return err
		}
		seq, err := store.EnqueueFollowUpTx(tx, sessionID, prompt)
		if err != nil {
			return err
		}
		if !hasUnclaimedFollowUp(j) {
			if err := applyFollowUpTx(tx, j, sessionID, seq, prompt); err != nil {
				return err
			}
		}
		if j.State != "leased" {
			j.State = "queued"
		}
		if err := store.UpdateJobTx(tx, j); err != nil {
			return err
		}
		turnID = j.ID
		pending = j.PendingRevision
		return nil
	})
	return turnID, pending, err
}

func hasUnclaimedFollowUp(j *store.Job) bool {
	if j.PendingRevision <= j.ClaimedRevision {
		return false
	}
	return j.ClaimedRevision != 0 || j.PendingRevision != 1
}

func applyFollowUpTx(tx *sql.Tx, j *store.Job, sessionID int64, seq int, prompt string) error {
	rev := j.PendingRevision + 1
	it := snapshot.Item{
		Repo: j.Repo, Item: j.Item, ItemKind: j.ItemKind, State: "open",
		Title: "follow-up", Body: prompt, DefaultBranch: "main",
	}
	if pend, err := store.LoadSnapshotTx(tx, j.Repo, j.Item, j.PendingRevision); err == nil {
		it.MainSHA = pend.MainSHA
		it.HeadSHA = pend.HeadSHA
		it.DefaultBranch = pend.DefaultBranch
	}
	if err := store.SaveSnapshotTx(tx, j.Repo, j.Item, rev, it); err != nil {
		return err
	}
	if err := store.SetSessionPromptTx(tx, sessionID, prompt); err != nil {
		return err
	}
	if err := store.ConsumeFollowUpTx(tx, sessionID, seq); err != nil {
		return err
	}
	j.PendingRevision = rev
	j.RetryCount = 0
	return nil
}

func applyNextFollowUpTx(tx *sql.Tx, j *store.Job) error {
	turn, err := store.GetTurnTx(tx, j.ID)
	if err != nil {
		return err
	}
	seq, prompt, ok, err := store.NextFollowUpTx(tx, turn.SessionID)
	if err != nil || !ok {
		return err
	}
	return applyFollowUpTx(tx, j, turn.SessionID, seq, prompt)
}

package engine

import (
	"database/sql"
	"encoding/json"
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
		if err := enqueueFollowUpTx(tx, j, sessionID, prompt); err != nil {
			return err
		}
		turnID = j.ID
		pending = j.PendingRevision
		return nil
	})
	return turnID, pending, err
}

type Steer struct {
	ID     int64  `json:"id"`
	Prompt string `json:"prompt"`
}

func (e *Engine) PromptSteer(sessionID int64, prompt string) (int64, int, bool, error) {
	if prompt == "" {
		return 0, 0, false, fmt.Errorf("prompt required")
	}
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return 0, 0, false, err
	}
	turns, err := store.ListTurnsForSession(e.Store, sessionID)
	if err != nil {
		return 0, 0, false, err
	}
	if len(turns) == 0 {
		return 0, 0, false, fmt.Errorf("no turns")
	}
	var turnID int64
	var pending int
	var live bool
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
		now := e.now()
		live = j.State == "leased" && j.LeaseGeneration > 0 &&
			j.LeaseExpiresAt != nil && now.Before(*j.LeaseExpiresAt) &&
			j.ExecutionDeadlineAt != nil && now.Before(*j.ExecutionDeadlineAt)
		turnID = j.ID
		if live {
			id, err := store.EnqueueSteerTx(tx, sessionID, j.ID, j.LeaseGeneration, prompt)
			if err != nil {
				return err
			}
			pending = j.PendingRevision
			return insertOperatorSteerTx(tx, id, sess, j, prompt, "steer")
		}
		seq, err := store.EnqueueFollowUpTx(tx, sessionID, prompt)
		if err != nil {
			return err
		}
		earlierSteer, err := store.HasEarlierUnacknowledgedSteerTx(tx, sessionID, seq)
		if err != nil {
			return err
		}
		if !hasUnclaimedFollowUp(j) && !earlierSteer {
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
		pending = j.PendingRevision
		return insertOperatorSteerTx(tx, int64(seq), sess, j, prompt, "follow_up")
	})
	return turnID, pending, live, err
}

func insertOperatorSteerTx(tx *sql.Tx, id int64, sess *store.Session, j *store.Job, prompt, delivery string) error {
	body, err := json.Marshal(map[string]string{"actor": "operator", "delivery": delivery, "prompt": prompt})
	if err != nil {
		return err
	}
	sessionID, turnID := sess.ID, j.ID
	return store.InsertActionTx(tx, store.Action{
		ID: fmt.Sprintf("operator-steer-%s-%d-%d", delivery, sessionID, id), SessionID: &sessionID, TurnID: &turnID,
		Repo: sess.Repo, Item: sess.Item, Type: "operator.steer", ReasonCode: "operator_input",
		EvidenceClass: "operator_input",
		LimitSentence: "Operator supplied this prompt; it does not approve pending permissions or bypass the tool fence.",
		Body:          string(body),
	})
}

func hasUnclaimedFollowUp(j *store.Job) bool {
	if j.PendingRevision <= j.ClaimedRevision {
		return false
	}
	return j.ClaimedRevision != 0 || j.PendingRevision != 1
}

func enqueueFollowUpTx(tx *sql.Tx, j *store.Job, sessionID int64, prompt string) error {
	seq, err := store.EnqueueFollowUpTx(tx, sessionID, prompt)
	if err != nil {
		return err
	}
	earlierSteer, err := store.HasEarlierUnacknowledgedSteerTx(tx, sessionID, seq)
	if err != nil {
		return err
	}
	if !hasUnclaimedFollowUp(j) && !earlierSteer {
		if err := applyFollowUpTx(tx, j, sessionID, seq, prompt); err != nil {
			return err
		}
	}
	if j.State != "leased" {
		j.State = "queued"
	}
	return store.UpdateJobTx(tx, j)
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

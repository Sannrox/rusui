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
		if _, err := enqueuePromptTx(tx, j, sessionID, prompt, false); err != nil {
			return err
		}
		turnID = j.ID
		pending = j.PendingRevision
		return nil
	})
	return turnID, pending, err
}

// PromptQueued stores a prompt that starts as the next turn once the
// current one ends (#492). Unlike a follow-up it does not become the
// pending revision while the turn runs, so DropQueuedPrompts or a session
// cancel can still drop it. held reports that it waits; false means no
// turn was running and the prompt is already the pending revision.
func (e *Engine) PromptQueued(sessionID int64, prompt string) (int64, int, bool, error) {
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
	var held bool
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
		seq, err := enqueuePromptTx(tx, j, sessionID, prompt, true)
		if err != nil {
			return err
		}
		next, ok, err := store.NextFollowUpTx(tx, sessionID)
		if err != nil {
			return err
		}
		held = ok && next.Seq <= seq
		turnID = j.ID
		pending = j.PendingRevision
		return insertOperatorSteerTx(tx, int64(seq), sess, j, prompt, "queued")
	})
	return turnID, pending, held, err
}

// DropQueuedPrompts drops the session's queued prompts that have not
// started. The current turn keeps running; ordinary follow-ups and
// promoted steers stay in the FIFO.
func (e *Engine) DropQueuedPrompts(sessionID int64) (int, error) {
	turns, err := store.ListTurnsForSession(e.Store, sessionID)
	if err != nil {
		return 0, err
	}
	if len(turns) == 0 {
		return 0, fmt.Errorf("no turns")
	}
	var n int
	err = e.Store.Tx(func(tx *sql.Tx) error {
		var err error
		n, err = store.DropQueuedPromptsTx(tx, sessionID)
		if err != nil || n == 0 {
			return err
		}
		j, err := store.GetJobByIDTx(tx, turns[0].ID)
		if err != nil {
			return err
		}
		// A follow-up that waited behind a dropped prompt takes its place.
		before := j.PendingRevision
		if err := promoteFollowUpTx(tx, j, sessionID); err != nil {
			return err
		}
		if j.PendingRevision == before {
			return nil
		}
		if j.State != "leased" {
			j.State = "queued"
		}
		return store.UpdateJobTx(tx, j)
	})
	return n, err
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
		seq, err := enqueuePromptTx(tx, j, sessionID, prompt, false)
		if err != nil {
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

// enqueuePromptTx appends the prompt to the session FIFO, then lets the
// FIFO head become the pending revision when nothing is ahead of it.
func enqueuePromptTx(tx *sql.Tx, j *store.Job, sessionID int64, prompt string, queued bool) (int, error) {
	enqueue := store.EnqueueFollowUpTx
	if queued {
		enqueue = store.EnqueueQueuedPromptTx
	}
	seq, err := enqueue(tx, sessionID, prompt)
	if err != nil {
		return 0, err
	}
	if err := promoteFollowUpTx(tx, j, sessionID); err != nil {
		return 0, err
	}
	if j.State != "leased" {
		j.State = "queued"
	}
	return seq, store.UpdateJobTx(tx, j)
}

// promoteFollowUpTx makes the FIFO head the pending revision unless an
// earlier revision or steer is still owed. A queued head waits while the
// turn is leased or waiting for a claim; the turn ending applies it.
func promoteFollowUpTx(tx *sql.Tx, j *store.Job, sessionID int64) error {
	if hasUnclaimedFollowUp(j) {
		return nil
	}
	next, ok, err := store.NextFollowUpTx(tx, sessionID)
	if err != nil || !ok {
		return err
	}
	if next.Queued && (j.State == "leased" || j.State == "queued") {
		return nil
	}
	earlierSteer, err := store.HasEarlierUnacknowledgedSteerTx(tx, sessionID, next.Seq)
	if err != nil || earlierSteer {
		return err
	}
	return applyFollowUpTx(tx, j, sessionID, next.Seq, next.Prompt)
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
	next, ok, err := store.NextFollowUpTx(tx, turn.SessionID)
	if err != nil || !ok {
		return err
	}
	return applyFollowUpTx(tx, j, turn.SessionID, next.Seq, next.Prompt)
}

package engine

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

// StartChild starts one child run of parentID in the same project
// (ADR 0071 D1). The child gets a new environment from a snapshot.
func (e *Engine) StartChild(parentID int64, prompt string) (int64, error) {
	if prompt == "" {
		return 0, fmt.Errorf("prompt required")
	}
	parent, err := store.GetSession(e.Store, parentID)
	if err != nil {
		return 0, err
	}
	if parent.Archived {
		return 0, errArchived
	}
	if parent.Kind != store.SessionKindRun && parent.Kind != store.SessionKindScheduled {
		return 0, fmt.Errorf("parent kind")
	}
	if parent.ParentSessionID != 0 {
		return 0, fmt.Errorf("depth")
	}
	p, ok := e.PolicySnapshot().Project(parent.Project)
	if !ok || !p.AllowsKind(policy.KindRun) {
		return 0, fmt.Errorf("policy")
	}
	var childID int64
	err = e.Store.Tx(func(tx *sql.Tx) error {
		paused, err := store.Paused(tx, parent.Project)
		if err != nil {
			return err
		}
		if paused {
			return errPaused
		}
		n, err := store.CountChildSessionsTx(tx, parentID)
		if err != nil {
			return err
		}
		if n >= p.MaxConcurrentLeases() {
			return fmt.Errorf("fan-out")
		}
		sid, item, err := store.InsertChildSessionTx(tx, parent.Project, parent.Repo, prompt, parentID)
		if err != nil {
			return err
		}
		if parent.Mode != "" {
			if err := store.SetSessionModeTx(tx, sid, parent.Mode); err != nil {
				return err
			}
		}
		sha := ""
		if d, ok := e.GitHub.(interface {
			DefaultSHA(string) (string, error)
		}); ok {
			sha, _ = d.DefaultSHA(parent.Repo)
		}
		it := snapshot.Item{
			Repo: parent.Repo, Item: item, ItemKind: "run", State: "open",
			Title: "run", Body: prompt, DefaultBranch: "main", MainSHA: sha,
		}
		if err := store.SaveSnapshotTx(tx, parent.Repo, item, 1, it); err != nil {
			return err
		}
		if err := insertChildSpawnTx(tx, parent, sid, prompt); err != nil {
			return err
		}
		childID = sid
		return nil
	})
	return childID, err
}

func insertChildSpawnTx(tx *sql.Tx, parent *store.Session, childID int64, prompt string) error {
	body, err := json.Marshal(map[string]any{"child_session_id": childID, "prompt": prompt})
	if err != nil {
		return err
	}
	parentID := parent.ID
	return store.InsertActionTx(tx, store.Action{
		ID: fmt.Sprintf("child-spawn-%d-%d", parentID, childID), SessionID: &parentID,
		Repo: parent.Repo, Item: parent.Item, Type: "child.spawn", ReasonCode: "recorded",
		EvidenceClass: "plane_observed",
		LimitSentence: "A child session started in the same project; it cannot widen policy, repository, kind, or egress.",
		Body:          string(body),
	})
}

func (e *Engine) recordChildOutcomeTx(tx *sql.Tx, turnID int64, outcome string) error {
	turn, err := store.GetTurnTx(tx, turnID)
	if err != nil {
		return err
	}
	parentID, repo, item, err := store.SessionLineageTx(tx, turn.SessionID)
	if err != nil || parentID == 0 {
		return err
	}
	body, err := json.Marshal(map[string]any{"child_session_id": turn.SessionID, "outcome": outcome})
	if err != nil {
		return err
	}
	return store.InsertActionTx(tx, store.Action{
		ID: fmt.Sprintf("child-result-%d-%s", turn.SessionID, outcome), SessionID: &parentID,
		Repo: repo, Item: item, Type: "child.result", ReasonCode: "recorded",
		EvidenceClass: "plane_observed",
		LimitSentence: "Child failure is a recorded result on the parent; it does not retry the child.",
		Body:          string(body),
	})
}

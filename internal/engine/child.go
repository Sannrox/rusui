package engine

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

// StartChild starts one child run of parentID. An empty project keeps
// the parent's project (ADR 0071 D1). A named project must already
// exist in policy; the child is admitted under that project's policy
// and does not inherit the parent's grants (ADR 0072).
func (e *Engine) StartChild(parentID int64, prompt, project string) (int64, error) {
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
	if project == "" {
		project = parent.Project
	}
	pol := e.PolicySnapshot()
	parentPol, ok := pol.Project(parent.Project)
	if !ok {
		return 0, fmt.Errorf("policy")
	}
	p, ok := pol.Project(project)
	if !ok || !p.AllowsKind(policy.KindRun) {
		return 0, fmt.Errorf("policy")
	}
	// Same-project children stay on the parent's pin so a parent that is
	// not Repos[0], or a later repo reorder, cannot move the child.
	var repo, sha string
	if project == parent.Project {
		repo = parent.Repo
		if d, ok := e.GitHub.(interface {
			DefaultSHA(string) (string, error)
		}); ok {
			sha, _ = d.DefaultSHA(repo)
		}
	} else {
		repo, sha = e.operatorSessionPin(p)
	}
	var childID int64
	err = e.Store.Tx(func(tx *sql.Tx) error {
		for _, slug := range []string{parent.Project, project} {
			paused, err := store.Paused(tx, slug)
			if err != nil {
				return err
			}
			if paused {
				return errPaused
			}
		}
		n, err := store.CountChildSessionsTx(tx, parentID)
		if err != nil {
			return err
		}
		if n >= parentPol.MaxConcurrentLeases() {
			return fmt.Errorf("fan-out")
		}
		sid, item, err := store.InsertChildSessionTx(tx, project, repo, prompt, parentID)
		if err != nil {
			return err
		}
		if err := store.SetSessionSizeTx(tx, sid, NormalizeSize(p.Size)); err != nil {
			return err
		}
		if parent.Mode != "" {
			if err := store.SetSessionModeTx(tx, sid, parent.Mode); err != nil {
				return err
			}
		}
		it := snapshot.Item{
			Repo: repo, Item: item, ItemKind: "run", State: "open",
			Title: "run", Body: prompt, DefaultBranch: "main", MainSHA: sha,
		}
		if err := store.SaveSnapshotTx(tx, repo, item, 1, it); err != nil {
			return err
		}
		if err := insertChildSpawnTx(tx, parent, sid, project, prompt); err != nil {
			return err
		}
		childID = sid
		return nil
	})
	return childID, err
}

func insertChildSpawnTx(tx *sql.Tx, parent *store.Session, childID int64, project, prompt string) error {
	body, err := json.Marshal(map[string]any{
		"child_session_id": childID, "project": project, "prompt": prompt,
	})
	if err != nil {
		return err
	}
	parentID := parent.ID
	return store.InsertActionTx(tx, store.Action{
		ID: fmt.Sprintf("child-spawn-%d-%d", parentID, childID), SessionID: &parentID,
		Repo: parent.Repo, Item: parent.Item, Type: "child.spawn", ReasonCode: "recorded",
		EvidenceClass: "plane_observed",
		LimitSentence: "A child session started; it is admitted under the named project's policy and does not inherit the parent's grants.",
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
	var childProject string
	if err := tx.QueryRow(`SELECT project FROM sessions WHERE id=?`, turn.SessionID).Scan(&childProject); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"child_session_id": turn.SessionID, "project": childProject, "outcome": outcome,
	})
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

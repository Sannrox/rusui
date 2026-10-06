package engine

import (
	"database/sql"
	"fmt"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

func (e *Engine) StartRun(project, prompt, idem string) (int64, error) {
	return e.StartRunSize(project, prompt, idem, "")
}

func (e *Engine) StartRunSize(project, prompt, idem, size string) (int64, error) {
	p, ok := e.PolicySnapshot().Project(project)
	if !ok || !p.AllowsKind(policy.KindRun) {
		return 0, fmt.Errorf("policy")
	}
	if size != "" {
		if NormalizeSize(size) == "" || size != NormalizeSize(size) {
			return 0, fmt.Errorf("size")
		}
	}
	if prompt == "" {
		return 0, fmt.Errorf("prompt required")
	}
	repo, sha := e.operatorSessionPin(p)
	var sessionID int64
	err := e.Store.Tx(func(tx *sql.Tx) error {
		paused, err := store.Paused(tx, project)
		if err != nil {
			return err
		}
		if paused {
			return errPaused
		}
		if idem != "" {
			id, ok, err := store.LookupIdempotencyTx(tx, idem)
			if err != nil {
				return err
			}
			if ok {
				sessionID = id
				return nil
			}
		}
		sid, item, err := store.InsertRunSessionTx(tx, project, repo, prompt)
		if err != nil {
			return err
		}
		resolved := size
		if resolved == "" {
			resolved = p.Size
		}
		if err := store.SetSessionSizeTx(tx, sid, NormalizeSize(resolved)); err != nil {
			return err
		}
		it := snapshot.Item{
			Repo: repo, Item: item, ItemKind: "run", State: "open",
			Title: "run", Body: prompt, DefaultBranch: "main", MainSHA: sha,
		}
		if err := store.SaveSnapshotTx(tx, repo, item, 1, it); err != nil {
			return err
		}
		if idem != "" {
			if err := store.PutIdempotencyTx(tx, idem, sid); err != nil {
				return err
			}
		}
		sessionID = sid
		return nil
	})
	return sessionID, err
}

// operatorSessionPin is the sessions.repo value and optional git pin SHA
// for a run or scheduled session. A project with no bound repository uses
// the reserved project key and an empty pin (ADR 0048).
func (e *Engine) operatorSessionPin(p policy.Project) (repo, sha string) {
	if len(p.Repos) == 0 {
		return policy.ProjectKey(p.Slug), ""
	}
	repo = p.Repos[0]
	if d, ok := e.GitHub.(interface {
		DefaultSHA(string) (string, error)
	}); ok {
		sha, _ = d.DefaultSHA(repo)
	}
	return repo, sha
}

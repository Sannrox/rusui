package engine

import (
	"database/sql"
	"fmt"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

func (e *Engine) StartRun(project, prompt, idem string) (int64, error) {
	p, ok := e.Policy.Project(project)
	if !ok || !p.AllowsKind(policy.KindRun) {
		return 0, fmt.Errorf("policy")
	}
	if prompt == "" {
		return 0, fmt.Errorf("prompt required")
	}
	if len(p.Repos) == 0 {
		return 0, fmt.Errorf("project has no bound repo")
	}
	repo := p.Repos[0]
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
		sha := ""
		if d, ok := e.GitHub.(interface {
			DefaultSHA(string) (string, error)
		}); ok {
			sha, _ = d.DefaultSHA(repo)
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

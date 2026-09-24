package engine

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

const MinScheduleEvery = time.Minute

func (e *Engine) CreateSchedule(project, name, every, prompt string) (int64, error) {
	p, ok := e.PolicySnapshot().Project(project)
	if !ok || !p.AllowsKind(policy.KindScheduled) {
		return 0, fmt.Errorf("policy")
	}
	if name == "" || prompt == "" {
		return 0, fmt.Errorf("name and prompt required")
	}
	d, err := time.ParseDuration(every)
	if err != nil || d < MinScheduleEvery {
		return 0, fmt.Errorf("every must be a duration >= 1m")
	}
	return store.InsertSchedule(e.Store, project, name, int(d.Seconds()), prompt)
}

func (e *Engine) StepSchedules(now time.Time) error {
	list, err := store.ListSchedules(e.Store)
	if err != nil {
		return err
	}
	now = now.UTC()
	for _, sc := range list {
		if sc.EverySeconds <= 0 {
			continue
		}
		bucket := now.Unix() / int64(sc.EverySeconds)
		idem := fmt.Sprintf("sched/%d/%d", sc.ID, bucket)
		if _, err := e.StartScheduled(sc, idem); err != nil && err != errPaused {
			return err
		}
	}
	return nil
}

func (e *Engine) StartScheduled(sc store.Schedule, idem string) (int64, error) {
	p, ok := e.PolicySnapshot().Project(sc.Project)
	if !ok || !p.AllowsKind(policy.KindScheduled) {
		return 0, fmt.Errorf("policy")
	}
	if len(p.Repos) == 0 {
		return 0, fmt.Errorf("project has no bound repo")
	}
	repo := p.Repos[0]
	var sessionID int64
	err := e.Store.Tx(func(tx *sql.Tx) error {
		paused, err := store.Paused(tx, sc.Project)
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
		live, err := store.ScheduleHasLiveSession(tx, sc.ID)
		if err != nil {
			return err
		}
		if live {
			return nil
		}
		sid, item, err := store.InsertScheduledSessionTx(tx, sc.Project, repo, sc.Prompt, sc.ID)
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
			Repo: repo, Item: item, ItemKind: "scheduled", State: "open",
			Title: "scheduled:" + sc.Name, Body: sc.Prompt, DefaultBranch: "main", MainSHA: sha,
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

package engine

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

const MinScheduleEvery = time.Minute

func (e *Engine) CreateSchedule(project, name, every, prompt string, sessionID int64) (int64, error) {
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
	if sessionID != 0 {
		sess, err := store.GetSession(e.Store, sessionID)
		if err != nil {
			return 0, fmt.Errorf("schedule session: %w", err)
		}
		if sess.Project != project {
			return 0, fmt.Errorf("schedule session project mismatch")
		}
		if sess.Archived {
			return 0, fmt.Errorf("schedule session archived")
		}
		if sess.Kind != store.SessionKindRun && sess.Kind != store.SessionKindScheduled {
			return 0, fmt.Errorf("schedule session kind")
		}
	}
	return store.InsertSchedule(e.Store, project, name, int(d.Seconds()), prompt, sessionID)
}

func (e *Engine) DeleteSchedule(project string, id int64) error {
	if _, ok := e.PolicySnapshot().Project(project); !ok {
		return fmt.Errorf("policy")
	}
	return store.DeleteSchedule(e.Store, project, id)
}

func (e *Engine) StepSchedules(now time.Time) error {
	list, err := store.ListSchedules(e.Store)
	if err != nil {
		return err
	}
	now = now.UTC()
	var errs []error
	for _, sc := range list {
		if sc.EverySeconds <= 0 {
			continue
		}
		bucket := now.Unix() / int64(sc.EverySeconds)
		idem := fmt.Sprintf("sched/%d/%d", sc.ID, bucket)
		if _, err := e.StartScheduled(sc, idem); err != nil && err != errPaused {
			errs = append(errs, fmt.Errorf("schedule %d: %w", sc.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (e *Engine) StartScheduled(sc store.Schedule, idem string) (int64, error) {
	p, ok := e.PolicySnapshot().Project(sc.Project)
	if !ok || !p.AllowsKind(policy.KindScheduled) {
		return 0, fmt.Errorf("policy")
	}
	repo, sha := e.operatorSessionPin(p)
	var sessionID int64
	already := false
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
				already = true
				return nil
			}
		}
		if sc.SessionID != 0 {
			var project string
			var archived int
			err := tx.QueryRow(`SELECT project, archived FROM sessions WHERE id=?`, sc.SessionID).Scan(&project, &archived)
			if err != nil {
				return err
			}
			if project != sc.Project {
				return fmt.Errorf("schedule session project mismatch")
			}
			if archived != 0 {
				already = true
			}
			if idem != "" {
				if err := store.PutIdempotencyTx(tx, idem, sc.SessionID); err != nil {
					return err
				}
			}
			sessionID = sc.SessionID
			return nil
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
	if err != nil || already || sc.SessionID == 0 {
		return sessionID, err
	}
	_, _, _, qerr := e.PromptQueued(sessionID, sc.Prompt)
	return sessionID, qerr
}

package engine

import (
	"encoding/json"
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

// StepSchedules fires each schedule's current bucket once. fence is the
// scheduler lease; a fire after the lease is lost commits nothing.
func (e *Engine) StepSchedules(now time.Time, fence store.PlaneLease) error {
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
		// A refused fire is recorded as admit.refuse; like a paused one, it
		// is not a scheduler fault.
		var refused *AdmitRefusedError
		if _, err := e.StartScheduled(sc, idem, fence); err != nil && !errors.Is(err, errPaused) && !errors.Is(err, errHalted) && !errors.As(err, &refused) {
			errs = append(errs, fmt.Errorf("schedule %d: %w", sc.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (e *Engine) StartScheduled(sc store.Schedule, idem string, fence store.PlaneLease) (int64, error) {
	p, ok := e.PolicySnapshot().Project(sc.Project)
	if !ok || !p.AllowsKind(policy.KindScheduled) {
		return 0, fmt.Errorf("policy")
	}
	repo, sha := e.operatorSessionPin(p)
	var sessionID int64
	already := false
	err := e.admitStart(AdmitRequest{Project: sc.Project, Kind: store.SessionKindScheduled, Repo: repo, BaseSHA: sha, Actor: sessionTenant(p), PromptSHA256: promptDigest(sc.Prompt)}, idem, func(g *admitGate) error {
		return e.Store.Tx(func(tx *store.Tx) error {
			sessionID, already = 0, false // Tx may rerun this callback
			// A stale scheduler (paused past its lease) must not start a session.
			if err := fence.Check(tx); err != nil {
				return err
			}
			paused, err := store.Paused(tx, sc.Project)
			if err != nil {
				return err
			}
			if paused {
				return errPaused
			}
			halted, err := store.Halted(tx, sessionTenant(p), sc.Project, sessionTenant(p))
			if err != nil {
				return err
			}
			if halted {
				return errHalted
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
				project, archived, err := store.SessionProjectArchivedTx(tx, sc.SessionID)
				if err != nil {
					return err
				}
				if project != sc.Project {
					return fmt.Errorf("schedule session project mismatch")
				}
				if archived {
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
			if err := g.ready(); err != nil {
				return err
			}
			sid, item, err := store.InsertScheduledSessionTx(tx, sc.Project, repo, sc.Prompt, sc.ID)
			if err != nil {
				return err
			}
			if err := g.recordTx(tx, sid); err != nil {
				return err
			}
			if err := store.SetSessionTenantTx(tx, sid, sessionTenant(p)); err != nil {
				return err
			}
			if err := store.SetSessionExecutionTx(tx, sid, store.ExecutionSpace, ""); err != nil {
				return err
			}
			if err := e.applyDefaultRetentionTx(tx, sid); err != nil {
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
	})
	if err != nil || already || sc.SessionID == 0 {
		return sessionID, err
	}
	_, _, _, qerr := e.PromptQueued(sessionID, sc.Prompt)
	return sessionID, qerr
}

func guestScheduleName(sessionID int64) string {
	return fmt.Sprintf("guest-%d", sessionID)
}

// ScheduleRequest is a guest set, replace, or clear of this session's
// bound schedule. Policy files stay operator-owned (#511).
type ScheduleRequest struct {
	Op        string `json:"op"`
	SessionID int64  `json:"session_id"`
	Every     string `json:"every"`
	Prompt    string `json:"prompt"`
}

// ScheduleReceipt records the request with the session id, schedule, and prompt.
type ScheduleReceipt struct {
	ID         string `json:"id"`
	SessionID  int64  `json:"session_id"`
	ScheduleID int64  `json:"schedule_id,omitempty"`
	Op         string `json:"op"`
	Accepted   bool   `json:"accepted"`
	Every      string `json:"every,omitempty"`
	Prompt     string `json:"prompt,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

// GuestScheduleRequest commits a guest schedule change for the leased
// turn's session and writes a receipt. A request for another session,
// another project, or a disallowed kind is refused and receipted.
func (e *Engine) GuestScheduleRequest(turnID int64, req ScheduleRequest) (ScheduleReceipt, error) {
	switch req.Op {
	case "set", "replace", "clear":
	default:
		return ScheduleReceipt{}, fmt.Errorf("op")
	}
	var rec ScheduleReceipt
	err := e.Store.Tx(func(tx *store.Tx) error {
		turn, err := store.GetTurnTx(tx, turnID)
		if err != nil {
			return err
		}
		if turn.State != "leased" {
			return fmt.Errorf("%w: turn is %s", ErrTurnNotLeased, turn.State)
		}
		project, kind, archived, err := store.SessionProjectKindArchivedTx(tx, turn.SessionID)
		if err != nil {
			return err
		}
		target := req.SessionID
		if target == 0 {
			target = turn.SessionID
		}
		rec = ScheduleReceipt{Op: req.Op, SessionID: target, Every: req.Every, Prompt: req.Prompt}
		detail := ""
		if target != turn.SessionID {
			tProject, err := store.SessionProjectTx(tx, target)
			if err != nil && !errors.Is(err, store.ErrNoRows) {
				return err
			}
			if err == nil && tProject != project {
				detail = "another project"
			} else {
				detail = "another session"
			}
		} else if archived {
			detail = "archived"
		} else if kind != store.SessionKindRun && kind != store.SessionKindScheduled {
			detail = "kind"
		} else {
			p, ok := e.PolicySnapshot().Project(project)
			if !ok || !p.AllowsKind(policy.KindScheduled) {
				detail = "kind"
			}
		}
		var secs int
		if detail == "" && req.Op != "clear" {
			if req.Prompt == "" {
				detail = "prompt"
			} else {
				d, err := time.ParseDuration(req.Every)
				if err != nil || d < MinScheduleEvery {
					detail = "every"
				} else {
					secs = int(d.Seconds())
					rec.Every = req.Every
				}
			}
		}
		if detail != "" {
			rec.Accepted = false
			rec.Detail = detail
			return e.insertScheduleReceiptTx(tx, turn, &rec)
		}
		name := guestScheduleName(target)
		existing, err := store.ScheduleByNameTx(tx, project, name)
		if err != nil && !errors.Is(err, store.ErrNoRows) {
			return err
		}
		switch req.Op {
		case "clear":
			if err == nil {
				if err := store.DeleteScheduleTx(tx, project, existing.ID); err != nil {
					return err
				}
				rec.ScheduleID = existing.ID
				rec.Every = (time.Duration(existing.EverySeconds) * time.Second).String()
				rec.Prompt = existing.Prompt
			}
		default:
			if errors.Is(err, store.ErrNoRows) {
				id, err := store.InsertScheduleTx(tx, project, name, secs, req.Prompt, target, e.now())
				if err != nil {
					return err
				}
				rec.ScheduleID = id
			} else {
				if err := store.UpdateScheduleTx(tx, existing.ID, secs, req.Prompt); err != nil {
					return err
				}
				rec.ScheduleID = existing.ID
			}
		}
		rec.Accepted = true
		return e.insertScheduleReceiptTx(tx, turn, &rec)
	})
	return rec, err
}

func (e *Engine) insertScheduleReceiptTx(tx *store.Tx, turn *store.Turn, rec *ScheduleReceipt) error {
	id, err := newActionID()
	if err != nil {
		return err
	}
	rec.ID = id
	reason := "refused"
	if rec.Accepted {
		reason = "accepted"
	}
	body, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	sid, tid := turn.SessionID, turn.ID
	repo, item, err := store.SessionSourceTx(tx, turn.SessionID)
	if err != nil {
		return err
	}
	return store.InsertActionTx(tx, store.Action{
		ID:            id,
		SessionID:     &sid,
		TurnID:        &tid,
		Repo:          repo,
		Item:          item,
		Type:          "schedule.request",
		ReasonCode:    reason,
		EvidenceClass: "plane_observed",
		LimitSentence: "Guest schedule request; policy.yaml stays operator-owned.",
		Body:          string(body),
	})
}

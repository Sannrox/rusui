package engine

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/sannrox/rusui/internal/store"
)

// HaltReport is the operator view after a halt overlay is set.
type HaltReport struct {
	Halted  bool             `json:"halted"`
	Scope   string           `json:"scope"`
	Target  string           `json:"target"`
	Stopped []store.LiveTurn `json:"stopped"`
}

// Halt records a kill-switch overlay and stops leased turns for matching
// sessions. Queued turns stay queued so resume can continue from last state.
func (e *Engine) Halt(scope, target string, rec store.HaltRecord) (HaltReport, error) {
	if rec.Source == "" {
		rec.Source = store.HaltSourceOp
	}
	return e.applyHalt(scope, target, rec)
}

// ApplyFreeze maps a control-plane freeze onto the same halt overlay.
func (e *Engine) ApplyFreeze(scope, target string, rec store.HaltRecord) (HaltReport, error) {
	rec.Source = store.HaltSourcePlane
	return e.applyHalt(scope, target, rec)
}

// ResumeHalt clears the overlay. Queued work becomes claimable again.
func (e *Engine) ResumeHalt(scope, target string, rec store.HaltRecord) error {
	scope = store.NormalizeHaltScope(scope)
	target = strings.TrimSpace(target)
	if rec.Source == "" {
		rec.Source = store.HaltSourceOp
	}
	return e.Store.Tx(func(tx *sql.Tx) error {
		return store.ClearHaltTx(tx, scope, target, rec)
	})
}

func (e *Engine) applyHalt(scope, target string, rec store.HaltRecord) (HaltReport, error) {
	scope = store.NormalizeHaltScope(scope)
	target = strings.TrimSpace(target)
	if scope == "" || target == "" {
		return HaltReport{}, fmt.Errorf("halt target")
	}
	var repos []string
	if scope == store.HaltScopeProject {
		if p, ok := e.PolicySnapshot().Project(target); ok {
			repos = p.Repos
		}
	}
	// Hold environment operations before the overlay and lease fence
	// commit, matching cancel: Claim skips an environment under an
	// operation, so a concurrent unhalt cannot start a harness that
	// this halt still kills.
	var releases []func()
	defer func() {
		for _, release := range slices.Backward(releases) {
			release()
		}
	}()
	heldEnv := map[int64]struct{}{}
	if err := e.holdHaltEnvironments(scope, target, repos, heldEnv, &releases); err != nil {
		return HaltReport{}, err
	}
	var stopped []store.LiveTurn
	var killIDs []int64
	// Overlay commit shares sumikaMu with local start/restart so a
	// successful halt cannot be followed by a pending process launch.
	e.sumikaMu.Lock()
	err := e.Store.Tx(func(tx *sql.Tx) error {
		if err := store.SetHaltTx(tx, scope, target, rec); err != nil {
			return err
		}
		ids, err := store.ListOpenSessionIDsForHaltTx(tx, scope, target, repos)
		if err != nil {
			return err
		}
		for _, id := range ids {
			sess, err := store.GetSessionTx(tx, id)
			if err != nil {
				return err
			}
			turns, err := store.ListTurnsForSessionTx(tx, id)
			if err != nil {
				return err
			}
			hadLeased := false
			for _, turn := range turns {
				if turn.State != "leased" {
					continue
				}
				if err := e.haltLeasedTurnTx(tx, turn.ID); err != nil {
					return err
				}
				stopped = append(stopped, store.LiveTurn{
					TurnID: turn.ID, SessionID: id, Repo: sess.Repo, Project: sess.Project, Lane: turn.Lane, State: "queued",
				})
				hadLeased = true
			}
			if hadLeased {
				killIDs = append(killIDs, id)
			}
		}
		return nil
	})
	e.sumikaMu.Unlock()
	if err != nil {
		return HaltReport{}, err
	}
	for _, id := range killIDs {
		sess, err := store.GetSession(e.Store, id)
		if err != nil {
			e.exception(fmt.Sprintf("halt session=%d: %v", id, err))
			continue
		}
		if _, ok := heldEnv[sess.EnvironmentID]; !ok {
			e.exception(fmt.Sprintf("halt session=%d: environment %d not held; leases already released", id, sess.EnvironmentID))
			continue
		}
		if err := e.haltGuest(id); err != nil {
			e.exception(fmt.Sprintf("halt session=%d: %v", id, err))
		}
	}
	if stopped == nil {
		stopped = []store.LiveTurn{}
	}
	return HaltReport{Halted: true, Scope: scope, Target: target, Stopped: stopped}, nil
}

func (e *Engine) holdHaltEnvironments(scope, target string, repos []string, heldEnv map[int64]struct{}, releases *[]func()) error {
	var ids []int64
	err := e.Store.Tx(func(tx *sql.Tx) error {
		var err error
		ids, err = store.ListOpenSessionIDsForHaltTx(tx, scope, target, repos)
		return err
	})
	if err != nil {
		return err
	}
	seen := map[int64]struct{}{}
	for _, id := range ids {
		sess, err := store.GetSession(e.Store, id)
		if err != nil {
			return err
		}
		if sess.Kind == store.SessionKindLocal {
			continue
		}
		if _, dup := seen[sess.EnvironmentID]; dup {
			continue
		}
		envRow, envErr := store.GetEnvironment(e.Store, sess.EnvironmentID)
		if envErr != nil || envRow.Handle == "" {
			continue
		}
		seen[envRow.ID] = struct{}{}
		if release, ok := e.waitEnvironmentOperation(envRow.ID, e.cancelEnvWait()); ok {
			*releases = append(*releases, release)
			heldEnv[envRow.ID] = struct{}{}
		} else {
			e.exception(fmt.Sprintf("halt environment %d stayed busy for %s; fencing leases without stopping that guest", envRow.ID, e.cancelEnvWait()))
		}
	}
	return nil
}

// haltGuest stops a ready guest. The caller holds the environment operation.
func (e *Engine) haltGuest(sessionID int64) error {
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return err
	}
	envRow, envErr := store.GetEnvironment(e.Store, sess.EnvironmentID)
	if envErr != nil || envRow.Handle == "" || envRow.State != store.EnvReady {
		return nil
	}
	d, err := e.driverFor(envRow.Driver)
	if err != nil {
		return nil
	}
	k, ok := d.(guestKiller)
	if !ok {
		return nil
	}
	if err := k.KillGuest(envRow.Handle); err != nil {
		e.exception(fmt.Sprintf("halt session=%d: stop guest of environment %d: %v", sessionID, envRow.ID, err))
	}
	if err := e.startServicesFor(d, envRow.ID, envRow.Handle); err != nil {
		e.exception(fmt.Sprintf("halt session=%d: restart services of environment %d: %v", sessionID, envRow.ID, err))
	}
	return nil
}

func (e *Engine) haltLeasedTurnTx(tx *sql.Tx, turnID int64) error {
	j, err := store.GetJobByIDTx(tx, turnID)
	if err != nil {
		return err
	}
	if j.State != "leased" {
		return nil
	}
	previousGeneration := j.LeaseGeneration
	receipt := map[string]any{"kind": "halted"}
	rb, _ := json.Marshal(receipt)
	if err := store.InsertReceiptTx(tx, j.ID, previousGeneration, j.ClaimedRevision, "cancelled", string(rb)); err != nil {
		return err
	}
	steers, err := store.PromoteSteersTx(tx, j.ID, previousGeneration)
	if err != nil {
		return err
	}
	j.LeaseGeneration++
	j.State = "queued"
	if steers > 0 {
		j.RetryCount = 0
		if j.ClaimedRevision == j.PendingRevision {
			if err := applyNextFollowUpTx(tx, j); err != nil {
				return err
			}
		}
	}
	if err := store.UpdateJobTx(tx, j); err != nil {
		return err
	}
	turn, err := store.GetTurnTx(tx, j.ID)
	if err != nil {
		return err
	}
	return store.TouchSessionEnvironmentTx(tx, turn.SessionID, e.now(), e.now().Add(e.envTTL()))
}

package engine

import (
	"fmt"
	"time"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

// Recover runs the startup recovery paths ARCHITECTURE.md requires:
// mark local runtime observations unknown, expire in-flight refresh owners,
// fail or requeue overdue leases, reconcile hook deliveries, catch up
// open and locally tracked items, and reap environments. It runs on every
// instance, so it leaves apply retries, which write to GitHub, to the
// scheduler lease holder (RetryApplyAttempts).
// Periodic callers use the same ReconcileConfigured and CatchUpConfigured
// methods. Errors besides owner expiry are
// reported through Notify and do not prevent the remaining paths.
func (e *Engine) Recover() error {
	if err := store.MarkRuntimeObservationsUnknown(e.Store, e.now()); err != nil {
		e.exception("startup invalidate runtime observations: " + err.Error())
		return err
	}
	if err := e.ReconcileSumika(); err != nil {
		e.exception("Sumika startup reconcile: " + err.Error())
	}
	if err := e.StartupExpireOwners(); err != nil {
		e.exception("startup expire owners: " + err.Error())
		return err
	}
	if err := e.ExpireOverdueLeases(); err != nil {
		e.exception("expire overdue leases: " + err.Error())
		return err
	}
	_ = e.ReconcileConfigured()
	if err := e.CatchUpConfigured(); err != nil {
		e.exception("catch-up: " + err.Error())
	}
	if err := e.ReapEnvironments(); err != nil {
		e.exception("reap environments: " + err.Error())
	}
	return nil
}

// ReconcileConfigured walks policy repos that have a hook id. When
// HookIDs is empty the path is skipped with a Notify reason rather than
// failing the scheduler.
func (e *Engine) ReconcileConfigured() error {
	if len(e.HookIDs) == 0 {
		e.exception("delivery reconcile skipped: RUSUI_GITHUB_HOOK_IDS is unset")
		return nil
	}
	pol := e.PolicySnapshot()
	var first error
	for repo, r := range pol.Repos {
		if r.Provider == policy.ProviderGitLab {
			continue
		}
		if e.HookIDs[repo] == "" {
			e.exception("delivery reconcile skipped for " + repo + ": no hook id")
			continue
		}
		if err := e.ReconcileDeliveries(repo); err != nil {
			e.exception("reconcile " + repo + ": " + err.Error())
			if first == nil {
				first = err
			}
		}
	}
	return first
}

// CatchUpConfigured lists open items per policy repo and also refreshes
// locally tracked non-terminal items (including items closed during downtime).
func (e *Engine) CatchUpConfigured() error {
	var open []snapshot.Item
	pol := e.PolicySnapshot()
	for repo, r := range pol.Repos {
		if r.Provider == policy.ProviderGitLab {
			continue
		}
		items, err := e.GitHub.ListOpenItems(repo)
		if err != nil {
			e.exception("list open " + repo + ": " + err.Error())
			continue
		}
		open = append(open, items...)
	}
	if err := e.catchUpGitLab(&open); err != nil {
		e.exception("gitlab catch-up: " + err.Error())
	}
	return e.CatchUpOpenAndLocal(open)
}

func (e *Engine) catchUpGitLab(open *[]snapshot.Item) error {
	if e.GitLab == nil {
		return nil
	}
	pol := e.PolicySnapshot()
	var first error
	for repo, r := range pol.Repos {
		if r.Provider != policy.ProviderGitLab {
			continue
		}
		var after time.Time
		_ = e.Store.Tx(func(tx *store.Tx) error {
			at, _, err := store.ReconcileCheckpointTx(tx, repo)
			if err != nil || at == "" {
				return nil
			}
			t, err := time.Parse(time.RFC3339, at)
			if err == nil {
				after = t.Add(-2 * time.Minute)
			}
			return nil
		})
		items, err := e.GitLab.ListIssues(r.ProjectID, r.ProjectPath, after)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		var newest time.Time
		for _, it := range items {
			*open = append(*open, it)
			if t, err := time.Parse(time.RFC3339, it.UpdatedAt); err == nil && t.After(newest) {
				newest = t
			}
		}
		if newest.IsZero() {
			continue
		}
		_ = e.Store.Tx(func(tx *store.Tx) error {
			return store.AdvanceReconcileCheckpointTx(tx, repo, newest.UTC().Format(time.RFC3339), "")
		})
	}
	return first
}

// RetryApplyAttempts retries apply rows left planned, in_flight, or
// uncertain, under the same action_id. It writes to GitHub, so it runs only
// under the scheduler lease: fence is checked before each item, and a lost
// lease stops the pass (ADR 0089 D6). Startup recovery leaves it to the
// scheduler, which runs it as soon as it holds the lease.
func (e *Engine) RetryApplyAttempts(fence store.PlaneLease) error {
	items, err := store.ListRetryableApply(e.Store)
	if err != nil {
		e.exception("list apply retries: " + err.Error())
		return err
	}
	var first error
	for _, it := range items {
		if err := e.Store.Tx(fence.Check); err != nil {
			return err
		}
		if err := e.ApplyAttempt(it.Repo, it.Item); err != nil {
			e.exception(fmt.Sprintf("apply retry %s#%d: %v", it.Repo, it.Item, err))
			if first == nil {
				first = err
			}
		}
	}
	return first
}

package engine

import (
	"fmt"

	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

// Recover runs the startup recovery paths ARCHITECTURE.md requires:
// expire in-flight refresh owners, reconcile hook deliveries, catch up
// open and locally tracked items, and retry unpublished apply attempts.
// Periodic callers use the same ReconcileConfigured, CatchUpConfigured,
// and RetryApplyAttempts methods. Errors besides owner expiry are
// reported through Notify and do not prevent the remaining paths.
func (e *Engine) Recover() error {
	if err := e.StartupExpireOwners(); err != nil {
		e.exception("startup expire owners: " + err.Error())
		return err
	}
	_ = e.ReconcileConfigured()
	if err := e.CatchUpConfigured(); err != nil {
		e.exception("catch-up: " + err.Error())
	}
	_ = e.RetryApplyAttempts()
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
	var first error
	for repo := range e.Policy.Repos {
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
	for repo := range e.Policy.Repos {
		items, err := e.GitHub.ListOpenItems(repo)
		if err != nil {
			e.exception("list open " + repo + ": " + err.Error())
			continue
		}
		open = append(open, items...)
	}
	return e.CatchUpOpenAndLocal(open)
}

// RetryApplyAttempts retries apply rows left planned, in_flight, or
// uncertain, under the same action_id.
func (e *Engine) RetryApplyAttempts() error {
	items, err := store.ListRetryableApply(e.Store)
	if err != nil {
		e.exception("list apply retries: " + err.Error())
		return err
	}
	var first error
	for _, it := range items {
		if err := e.ApplyAttempt(it.Repo, it.Item); err != nil {
			e.exception(fmt.Sprintf("apply retry %s#%d: %v", it.Repo, it.Item, err))
			if first == nil {
				first = err
			}
		}
	}
	return first
}

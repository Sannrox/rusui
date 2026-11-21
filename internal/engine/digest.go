package engine

import (
	"fmt"
	"time"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

// writeRepo is the bound repository with comment and repair narrowed when
// advisory_digest has been over the ADR 0038 D6 limit for three UTC days.
// Advisory (review) is unchanged.
func (e *Engine) writeRepo(repo string) (policy.Repo, bool) {
	pol, ok := e.PolicySnapshot().Repo(repo)
	if !ok {
		return pol, false
	}
	if !pol.AdvisoryDigest {
		return pol, true
	}
	items, err := store.InboxItems(e.Store, "")
	if err != nil {
		e.exception(fmt.Sprintf("advisory digest %s: %v", repo, err))
		pol.Comment = false
		pol.Repair = ""
		return pol, true
	}
	return narrowWriteRepo(repo, pol, items, e.now()), true
}

// applyWritePolicy is writeRepo inside an open store transaction.
func (e *Engine) applyWritePolicy(tx *store.Tx, repo string, pol policy.Repo) (policy.Repo, error) {
	if !pol.AdvisoryDigest {
		return pol, nil
	}
	items, err := store.InboxItemsTx(tx, "")
	if err != nil {
		return pol, err
	}
	return narrowWriteRepo(repo, pol, items, e.now()), nil
}

func narrowWriteRepo(repo string, pol policy.Repo, items []store.InboxItem, now time.Time) policy.Repo {
	if store.DigestStopsWrites(items, repo, now) {
		pol.Comment = false
		pol.Repair = ""
	}
	return pol
}

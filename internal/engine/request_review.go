package engine

import (
	"errors"
	"fmt"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

var (
	// ErrReviewRequestInvalid reports a missing repository or invalid PR number.
	ErrReviewRequestInvalid = errors.New("review requires a repository and pull request number")
	// ErrReviewRepoUnbound reports a repository outside the active policy.
	ErrReviewRepoUnbound = errors.New("repository is not bound by policy")
	// ErrReviewDisabled reports a repository with review disabled.
	ErrReviewDisabled = errors.New("review is disabled for repository")
	// ErrReviewPaused reports an active project pause.
	ErrReviewPaused = errors.New("project is paused")
	// ErrReviewBudget reports an exhausted daily review budget.
	ErrReviewBudget = errors.New("daily review budget exhausted")
	// ErrReviewRefreshBusy reports a refresh owned by another coordinator.
	ErrReviewRefreshBusy = errors.New("pull request refresh is already in progress")
	// ErrReviewRefreshFailed reports a PR that could not be refreshed.
	ErrReviewRefreshFailed = errors.New("pull request refresh failed")
	// ErrReviewIneligible reports a pull request outside the advisory profile.
	ErrReviewIneligible = errors.New("pull request is not eligible for advisory review")
)

// ReviewRequest identifies the session and current revision admitted for a PR.
type ReviewRequest struct {
	SessionID       int64
	TurnID          int64
	PendingRevision int
}

// RequestReview refreshes and admits one pull request through the normal
// refresh coordinator, then returns the existing session and pending turn.
func (e *Engine) RequestReview(repo string, item int) (ReviewRequest, error) {
	e.refreshMu.Lock()
	defer e.refreshMu.Unlock()

	if repo == "" || item <= 0 {
		return ReviewRequest{}, ErrReviewRequestInvalid
	}
	activePolicy := e.PolicySnapshot()
	decision := activePolicy.ReviewAccess(repo, false)
	switch decision.Reason {
	case policy.ReviewReasonUnboundRepo, policy.ReviewReasonMissingProject:
		return ReviewRequest{}, ErrReviewRepoUnbound
	case policy.ReviewReasonDisabled:
		return ReviewRequest{}, ErrReviewDisabled
	}
	repoPolicy, _ := activePolicy.Repo(repo)
	if !decision.Allowed {
		return ReviewRequest{}, ErrReviewRequestInvalid
	}
	if err := e.Store.Tx(func(tx *store.Tx) error {
		paused, err := store.Paused(tx, repoPolicy.Project)
		if err != nil {
			return err
		}
		if !activePolicy.ReviewAccess(repo, paused).Allowed {
			return ErrReviewPaused
		}
		return store.EnsureRefreshQueuedTx(tx, repo, item, "pull", false)
	}); err != nil {
		return ReviewRequest{}, err
	}

	did, err := e.stepRefresh(repo, item, "pull", true)
	if err != nil {
		return ReviewRequest{}, err
	}
	refresh, err := store.RefreshStatusOf(e.Store, repo, item)
	if err != nil {
		return ReviewRequest{}, err
	}
	refreshState, owner, retryCount := refresh.State, refresh.Owner, refresh.RetryCount
	if refreshState != "idle" || owner != 0 {
		if !did && owner != 0 {
			return ReviewRequest{}, ErrReviewRefreshBusy
		}
		if retryCount > 0 || refreshState == "failed" {
			return ReviewRequest{}, ErrReviewRefreshFailed
		}
		return ReviewRequest{}, ErrReviewRefreshBusy
	}

	var result ReviewRequest
	result.SessionID, err = store.ReviewSessionID(e.Store, repo, item)
	if errors.Is(err, store.ErrNoRows) {
		return ReviewRequest{}, ErrReviewRefreshFailed
	}
	if err != nil {
		return ReviewRequest{}, err
	}
	turns, err := store.ListTurnsForSession(e.Store, result.SessionID)
	if err != nil {
		return ReviewRequest{}, err
	}
	for _, turn := range turns {
		if turn.Lane == "review" {
			result.TurnID = turn.ID
			result.PendingRevision = turn.PendingRevision
		}
	}
	if result.TurnID == 0 || result.PendingRevision == 0 {
		return ReviewRequest{}, fmt.Errorf("review session has no pending turn")
	}
	return result, nil
}

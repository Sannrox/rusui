package policy

// ReviewDecision reports whether policy and runtime state permit a review.
type ReviewDecision struct {
	Allowed bool
	Reason  string
}

const (
	ReviewReasonAllowed        = "review is allowed by policy"
	ReviewReasonInvalidRepo    = "repository is required"
	ReviewReasonUnboundRepo    = "repository is not bound by policy"
	ReviewReasonMissingProject = "repository project is missing from policy"
	ReviewReasonDisabled       = "review is disabled for repository"
	ReviewReasonPaused         = "project is paused"
	ReviewReasonDailyBudget    = "daily review budget exhausted"
)

// ReviewAccess checks the policy and pause gates that apply to review work.
func (e *Effective) ReviewAccess(repo string, paused bool) ReviewDecision {
	if paused {
		return ReviewDecision{Reason: ReviewReasonPaused}
	}
	if repo == "" {
		return ReviewDecision{Reason: ReviewReasonInvalidRepo}
	}
	r, ok := e.Repo(repo)
	if !ok {
		return ReviewDecision{Reason: ReviewReasonUnboundRepo}
	}
	if _, ok := e.Project(r.Project); !ok {
		return ReviewDecision{Reason: ReviewReasonMissingProject}
	}
	if !r.Review {
		return ReviewDecision{Reason: ReviewReasonDisabled}
	}
	return ReviewDecision{Allowed: true, Reason: ReviewReasonAllowed}
}

// ReviewBudget checks the daily review count against the bound repository's cap.
func ReviewBudget(reviewsToday, limit int) ReviewDecision {
	if reviewsToday >= limit {
		return ReviewDecision{Reason: ReviewReasonDailyBudget}
	}
	return ReviewDecision{Allowed: true, Reason: ReviewReasonAllowed}
}

// DecideReview combines the review policy, pause, and daily budget gates.
func DecideReview(e *Effective, repo string, paused bool, reviewsToday int) ReviewDecision {
	access := e.ReviewAccess(repo, paused)
	if !access.Allowed {
		return access
	}
	r, _ := e.Repo(repo)
	return ReviewBudget(reviewsToday, r.MaxReviewsPerRepoPerUTCDay)
}

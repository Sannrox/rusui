package policy

import "testing"

const reviewDecisionPolicy = `version: 2
defaults:
  session_kinds: [review]
  review: true
  max_reviews_per_repo_per_utc_day: 2
projects:
  test:
    repos:
      example/repo: {}
`

const disabledReviewPolicy = `version: 2
defaults:
  session_kinds: [review]
projects:
  test:
    repos:
      example/repo:
        review: false
`

func TestDecideReview(t *testing.T) {
	effective, err := Parse([]byte(reviewDecisionPolicy))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name         string
		repo         string
		paused       bool
		reviewsToday int
		allowed      bool
		reason       string
	}{
		{name: "allow", repo: "example/repo", allowed: true, reason: ReviewReasonAllowed},
		{name: "unbound", repo: "other/repo", reason: ReviewReasonUnboundRepo},
		{name: "paused", repo: "example/repo", paused: true, reason: ReviewReasonPaused},
		{name: "daily budget", repo: "example/repo", reviewsToday: 2, reason: ReviewReasonDailyBudget},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DecideReview(effective, tt.repo, tt.paused, tt.reviewsToday)
			if got.Allowed != tt.allowed || got.Reason != tt.reason {
				t.Fatalf("DecideReview() = %+v, want allowed=%t reason=%q", got, tt.allowed, tt.reason)
			}
		})
	}
	disabled, err := Parse([]byte(disabledReviewPolicy))
	if err != nil {
		t.Fatal(err)
	}
	if got := DecideReview(disabled, "example/repo", false, 0); got.Reason != ReviewReasonDisabled {
		t.Fatal("disabled review did not refuse")
	}
}

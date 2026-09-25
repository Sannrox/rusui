package policy

import (
	"os"
	"testing"
)

const explanationPolicy = `version: 2
defaults:
  never_release: false
  never_leak_private_to_public: false
  session_kinds: [review, run]
  egress: trusted
  review: false
  comments: false
  max_reviews_per_repo_per_utc_day: 8
projects:
  test:
    session_kinds: []
    egress: ""
    budgets:
      max_concurrent_leases: 3
    repos:
      example/repo:
        review: true
`

func TestExplainRepoReportsEffectiveSources(t *testing.T) {
	effective, err := Parse([]byte(explanationPolicy))
	if err != nil {
		t.Fatal(err)
	}
	explanation, ok, err := effective.ExplainRepo("example/repo")
	if err != nil || !ok {
		t.Fatalf("ExplainRepo() = (%+v, %t, %v)", explanation, ok, err)
	}
	if !explanation.Repo.Review || explanation.Repo.Comments || explanation.Repo.MaxReviewsPerRepoPerUTCDay != 8 || !explanation.Repo.NeverRelease || !explanation.Repo.NeverLeakPrivateToPublic {
		t.Fatalf("effective repo values = %+v", explanation.Repo)
	}
	wantSources := map[string]string{
		"review":                           "repository override",
		"comments":                         "defaults",
		"max_reviews_per_repo_per_utc_day": "defaults",
		"never_release":                    "enforced by v2 parser",
		"never_leak_private_to_public":     "enforced by v2 parser",
		"session_kinds":                    "defaults",
		"egress":                           "defaults",
		"budgets.max_concurrent_leases":    "project",
		"permissions":                      "schema default",
		"local_runtime":                    "schema default",
	}
	for field, want := range wantSources {
		if got := explanation.Sources[field]; got != want {
			t.Errorf("source[%q] = %q, want %q", field, got, want)
		}
	}
}

func TestExplainRepoResolvesYAMLAliasesAndMergePrecedence(t *testing.T) {
	const raw = `version: 2
defaults:
  session_kinds: &default_kinds [review]
  egress: &default_egress trusted
projects:
  base: &project_defaults
    session_kinds: *default_kinds
    egress: *default_egress
    repos: {}
  test:
    <<: *project_defaults
    repos:
      example/base: &repo_defaults
        visibility: public
        review: false
        comments: true
      example/merged:
        <<: *repo_defaults
      example/direct:
        <<: *repo_defaults
        review: true
      example/sequence:
        <<:
          - {review: false, comments: true}
          - {review: true, comments: false}
`
	effective, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name           string
		repo           string
		wantReview     bool
		wantComments   bool
		wantVisibility string
	}{
		{name: "map merge", repo: "example/merged", wantReview: false, wantComments: true, wantVisibility: "public"},
		{name: "direct value overrides merge", repo: "example/direct", wantReview: true, wantComments: true, wantVisibility: "public"},
		{name: "sequence merge uses first mapping", repo: "example/sequence", wantReview: false, wantComments: true, wantVisibility: "private"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := effective.ExplainRepo(tt.repo)
			if err != nil || !ok {
				t.Fatalf("ExplainRepo() = (%+v, %t, %v)", got, ok, err)
			}
			if got.Repo.Review != tt.wantReview || got.Repo.Comments != tt.wantComments || got.Repo.Visibility != tt.wantVisibility {
				t.Fatalf("effective repo values = %+v", got.Repo)
			}
			if len(got.Project.SessionKinds) != 1 || got.Project.SessionKinds[0] != KindReview || got.Project.Egress != EgressTrusted {
				t.Fatalf("effective merged project values = %+v", got.Project)
			}
			for _, field := range []string{"review", "comments"} {
				if got.Sources[field] != "repository override" {
					t.Errorf("source[%q] = %q, want repository override", field, got.Sources[field])
				}
			}
			if got.Sources["session_kinds"] != "project" {
				t.Errorf("merged session_kinds source = %q, want project", got.Sources["session_kinds"])
			}
		})
	}
}

func TestStarterYAMLMatchesOperatorExample(t *testing.T) {
	contents, err := os.ReadFile("../../policy.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if string(StarterYAML()) != string(contents) {
		t.Fatal("policy starter drifted from policy.example.yaml")
	}
	effective, err := Parse(StarterYAML())
	if err != nil {
		t.Fatalf("starter policy does not parse: %v", err)
	}
	repo, ok := effective.Repo("Sannrox/rusui")
	if !ok || !repo.Review || repo.Comments || repo.Close || repo.Implement || repo.Land {
		t.Fatalf("starter capabilities are not review-only: %+v", repo)
	}
}

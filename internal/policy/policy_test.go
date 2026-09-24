package policy

import (
	"os"
	"strings"
	"testing"
)

func TestParseExampleAndFixture(t *testing.T) {
	for _, p := range []string{"../../policy.example.yaml", "../../policy.fixture.yaml"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		e, err := Parse(b)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if len(e.Repos) == 0 || len(e.Projects) == 0 {
			t.Fatal("no projects or repos")
		}
	}
}

func TestParseV2(t *testing.T) {
	raw := []byte(`
version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
  review: true
  comments: false
  close: false
  implement: false
  land: false
  max_reviews_per_repo_per_utc_day: 50
projects:
  rusui:
    repos:
      Sannrox/rusui:
        visibility: private
`)
	e, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := e.Repo("Sannrox/rusui")
	if !ok || r.Project != "rusui" || !r.Review || r.Comments {
		t.Fatalf("repo %+v ok=%v", r, ok)
	}
	p, ok := e.Project("rusui")
	if !ok || p.Egress != EgressTrusted || !p.AllowsKind(KindReview) {
		t.Fatalf("project %+v", p)
	}
	if p.MaxConcurrentLeases() != 1 {
		t.Fatalf("default concurrent leases %d", p.MaxConcurrentLeases())
	}
	if _, err := Parse([]byte("version: 1\nrepos: {}\n")); err == nil {
		t.Fatal("v1 accepted")
	}
}

func TestParseReviewDefaultAndRepoOverride(t *testing.T) {
	base := `
version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
projects:
  rusui:
    repos:
      Sannrox/rusui:
        visibility: private
`
	tests := []struct {
		name         string
		defaultFalse bool
		repoTrue     bool
		wantReview   bool
	}{
		{name: "default remains enabled when omitted", wantReview: true},
		{name: "explicit default disables review", defaultFalse: true, wantReview: false},
		{name: "repo override enables review", defaultFalse: true, repoTrue: true, wantReview: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := base
			if tt.defaultFalse {
				raw = strings.Replace(raw, "projects:\n", "  review: false\nprojects:\n", 1)
			}
			if tt.repoTrue {
				raw = strings.Replace(raw, "        visibility:", "        review: true\n        visibility:", 1)
			}
			e, err := Parse([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			r, ok := e.Repo("Sannrox/rusui")
			if !ok || r.Review != tt.wantReview {
				t.Fatalf("repo %+v ok=%v, want review=%v", r, ok, tt.wantReview)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	good := `
version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
projects:
  rusui:
    repos:
      Sannrox/rusui:
        visibility: private
`
	cases := []string{
		good + "  extra: 1\n",
		strings.Replace(good, "rusui:\n", "rusui:\n    egress: warp\n", 1),
		strings.Replace(good, "rusui:\n", "rusui:\n    session_kinds: [chat]\n", 1),
		good + `  other:
    repos:
      Sannrox/rusui:
        visibility: private
`,
		strings.Replace(good, "rusui:", "Rusui:", 1),
		strings.Replace(good, "rusui:\n", "rusui:\n    budgets: {tokens: 1}\n", 1),
		strings.Replace(good, "rusui:\n", "rusui:\n    budgets: {dollars: 1}\n", 1),
		strings.Replace(good, "rusui:\n", "rusui:\n    budgets: {max_concurrent_leases: 0}\n", 1),
		strings.Replace(good, "rusui:\n", "rusui:\n    permissions: [{command: npm, action: warp}]\n", 1),
		strings.Replace(good, "rusui:\n", "rusui:\n    permissions: [{action: reject}]\n", 1),
		strings.Replace(good, "rusui:\n", "rusui:\n    permissions: [{command: npm, verdict: reject}]\n", 1),
	}
	for i, c := range cases {
		if _, err := Parse([]byte(c)); err == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
}

func TestParseConcurrentLeaseBudget(t *testing.T) {
	raw := []byte(`
version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
projects:
  rusui:
    budgets:
      max_concurrent_leases: 3
    repos:
      Sannrox/rusui:
        visibility: public
`)
	e, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := e.Project("rusui")
	if !ok || p.MaxConcurrentLeases() != 3 {
		t.Fatalf("project %+v ok=%v", p, ok)
	}
}

func TestParseRejectRules(t *testing.T) {
	raw := []byte(`
version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
projects:
  rusui:
    permissions:
      - command: npm
      - command: npm publish
        action: reject
    repos:
      Sannrox/rusui:
        visibility: public
`)
	e, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := e.Project("rusui")
	if len(p.Permissions) != 2 || p.Permissions[1].Action != "reject" {
		t.Fatalf("permissions %+v", p.Permissions)
	}
}

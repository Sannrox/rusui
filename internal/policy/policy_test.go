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
		strings.Replace(good, "rusui:\n", "rusui:\n    ship: merge\n", 1),
		strings.Replace(good, "rusui:\n", "rusui:\n    connections: {xai: http://127.0.0.1:1}\n", 1),
		strings.Replace(good, "rusui:\n", "rusui:\n    budgets: {tokens: 1}\n", 1),
		strings.Replace(good, "rusui:\n", "rusui:\n    budgets: {dollars: 1}\n", 1),
		strings.Replace(good, "rusui:\n", "rusui:\n    budgets: {max_concurrent_leases: 0}\n", 1),
		strings.Replace(good, "rusui:\n", "rusui:\n    size: huge\n", 1),
		strings.Replace(good, "rusui:\n", "rusui:\n    secrets: [NPM]\n", 1),
		strings.Replace(good, "rusui:\n", "rusui:\n    secrets: [npm_token, npm_token]\n", 1),
		strings.Replace(good, "rusui:\n", "rusui:\n    secrets: ['']\n", 1),
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

func TestParseProjectSecrets(t *testing.T) {
	raw := []byte(`
version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
projects:
  rusui:
    secrets: [npm_token, gh_app]
    repos:
      Sannrox/rusui:
        visibility: public
`)
	e, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := e.Project("rusui")
	if !ok || len(p.Secrets) != 2 || p.Secrets[0] != "npm_token" || p.Secrets[1] != "gh_app" {
		t.Fatalf("project %+v ok=%v", p, ok)
	}
	if err := AllowSecrets(p.Secrets, []string{"npm_token"}); err != nil {
		t.Fatal(err)
	}
	if err := AllowSecrets(p.Secrets, []string{"aws_key"}); err == nil {
		t.Fatal("disallowed secret accepted")
	}
}

func TestParseProjectShip(t *testing.T) {
	raw := []byte(`
version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
projects:
  rusui:
    repos:
      Sannrox/rusui:
        visibility: public
`)
	e, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := e.Project("rusui")
	if !ok || p.Ship != ShipPullRequest {
		t.Fatalf("omitted ship %+v ok=%v", p, ok)
	}
	withPR := strings.Replace(string(raw), "rusui:\n", "rusui:\n    ship: pull-request\n", 1)
	e, err = Parse([]byte(withPR))
	if err != nil {
		t.Fatal(err)
	}
	p, ok = e.Project("rusui")
	if !ok || p.Ship != ShipPullRequest {
		t.Fatalf("pull-request %+v ok=%v", p, ok)
	}
	withPush := strings.Replace(string(raw), "rusui:\n", "rusui:\n    ship: push-base\n", 1)
	e, err = Parse([]byte(withPush))
	if err != nil {
		t.Fatal(err)
	}
	p, ok = e.Project("rusui")
	if !ok || p.Ship != ShipPushBase {
		t.Fatalf("push-base %+v ok=%v", p, ok)
	}
}

func TestParseProjectSize(t *testing.T) {
	raw := []byte(`
version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
projects:
  rusui:
    size: large
    repos:
      Sannrox/rusui:
        visibility: public
`)
	e, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := e.Project("rusui")
	if !ok || p.Size != "large" {
		t.Fatalf("project %+v ok=%v", p, ok)
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

func TestLocalRuntimeIsExplicitAndPolicyConfigured(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	base := `
version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
projects:
  local-test:
    repos: {}
`
	e, err := Parse([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := e.Project("local-test")
	if p.AllowsKind(KindLocal) || p.LocalRuntime != nil {
		t.Fatalf("local runtime defaulted on: %+v", p)
	}
	configured := base[:len(base)-1] + `
    session_kinds: [local]
    local_runtime:
      argv: ["/bin/sh", "-i"]
      cwd: "` + cwd + `"
`
	e, err = Parse([]byte(configured))
	if err != nil {
		t.Fatal(err)
	}
	p, _ = e.Project("local-test")
	if !p.AllowsKind(KindLocal) || p.LocalRuntime == nil || p.LocalRuntime.Cwd != cwd || len(p.LocalRuntime.Argv) != 2 || p.LocalRuntime.Argv[0] != "/bin/sh" {
		t.Fatalf("local runtime config %+v", p)
	}
	for _, invalid := range []string{
		base[:len(base)-1] + `
    session_kinds: [local]
`,
		base[:len(base)-1] + `
    local_runtime:
      argv: ["/bin/sh"]
      cwd: "` + cwd + `"
`,
		strings.Replace(configured, cwd, "relative/path", 1),
		strings.Replace(configured, "[\"/bin/sh\", \"-i\"]", "[]", 1),
		`version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled, local]
projects:
  local-test:
    repos: {}
`,
	} {
		if _, err := Parse([]byte(invalid)); err == nil {
			t.Fatalf("invalid local policy accepted:\n%s", invalid)
		}
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

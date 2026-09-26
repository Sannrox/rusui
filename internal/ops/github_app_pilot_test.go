package ops

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/policy"
)

func TestGitHubAppPilotGuideIsReviewOnly(t *testing.T) {
	root := filepath.Join("..", "..")
	guide := readRepoFile(t, root, "docs", "github-app-pilot.md")
	for _, want := range []string{
		"issues",
		"pull_request",
		"issue_comment",
		"RUSUI_GITHUB_APP_ID",
		"RUSUI_WEBHOOK_SECRET",
		"comments: false",
		"close: false",
		"implement: false",
		"land: false",
		"cloudflared tunnel --url http://127.0.0.1:8080",
		"X-Hub-Signature-256",
		"401",
		"sessions -url http://127.0.0.1:8080",
		"review_result",
		"Open-item catch-up",
	} {
		if !strings.Contains(guide, want) {
			t.Fatalf("github-app-pilot.md missing %q", want)
		}
	}
	if strings.Contains(guide, "include=receipts") {
		t.Fatal("pilot guide must not fetch environment receipts as the review artifact")
	}
	for _, secret := range []string{"ghp_", "ghs_", "BEGIN RSA PRIVATE KEY", "BEGIN PRIVATE KEY"} {
		if strings.Contains(guide, secret) {
			t.Fatalf("pilot guide must not embed credential fragment %q", secret)
		}
	}

	p, err := policy.Load(filepath.Join(root, "policy.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Repos) == 0 {
		t.Fatal("policy.example.yaml must bind a repository")
	}
	for name, repo := range p.Repos {
		if repo.Comments || repo.Close || repo.Implement || repo.Land {
			t.Fatalf("policy.example.yaml %s must disable writes: %+v", name, repo)
		}
		if !repo.Review {
			t.Fatalf("policy.example.yaml %s must keep review enabled", name)
		}
	}
}

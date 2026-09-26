package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

func TestPolicyInitCreatesStarterWithoutOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.yaml")
	var out, errOut bytes.Buffer
	if code := policyMain([]string{"init", "-file", path}, &out, &errOut); code != 0 {
		t.Fatalf("policy init code=%d stderr=%s", code, errOut.String())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != string(policy.StarterYAML()) {
		t.Fatal("policy init wrote unexpected starter contents")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("policy init file mode = %o, want 600", mode)
	}
	if code := policyMain([]string{"init", "-file", path}, &out, &errOut); code == 0 {
		t.Fatal("policy init overwrote an existing file")
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != string(contents) {
		t.Fatalf("existing policy changed: err=%v", err)
	}
}

func TestPolicySimulationMatchesServerReviewDecisionMatrix(t *testing.T) {
	tests := []struct {
		name         string
		repo         string
		review       bool
		limit        int
		used         int
		globalPause  bool
		projectPause bool
		allowed      bool
	}{
		{name: "allowed", repo: "example/repo", review: true, limit: 2, allowed: true},
		{name: "unbound repository", repo: "other/repo", review: true, limit: 2},
		{name: "review disabled", repo: "example/repo", review: false, limit: 2},
		{name: "global pause", repo: "example/repo", review: true, limit: 2, globalPause: true},
		{name: "project pause", repo: "example/repo", review: true, limit: 2, projectPause: true},
		{name: "daily budget exhausted", repo: "example/repo", review: true, limit: 1, used: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			policyPath := filepath.Join(dir, "policy.yaml")
			dbPath := filepath.Join(dir, "rusui.db")
			raw := []byte(fmt.Sprintf(`version: 2
defaults:
  session_kinds: [review]
  egress: trusted
  review: true
  max_reviews_per_repo_per_utc_day: %d
projects:
  test:
    repos:
      example/repo:
        review: %t
`, tt.limit, tt.review))
			if err := os.WriteFile(policyPath, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			effective, err := policy.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			stateStore, err := store.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stateStore.Close() }()
			now := time.Now().UTC()
			fakeGH := gh.NewFake()
			server := engine.New(stateStore, effective, fakeGH, &clock.Fake{T: now})
			server.ReloadPolicy(effective)
			if tt.globalPause {
				if err := server.SetPause("", true); err != nil {
					t.Fatal(err)
				}
			}
			if tt.projectPause {
				if err := server.SetPause("test", true); err != nil {
					t.Fatal(err)
				}
			}
			for range tt.used {
				if err := stateStore.Tx(func(tx *sql.Tx) error {
					return store.IncrReviewsToday(tx, "example/repo", now.Format("2006-01-02"))
				}); err != nil {
					t.Fatal(err)
				}
			}

			serverClaim, serverErr := server.Claim(tt.repo)
			if serverClaim != nil {
				t.Fatal("fixture unexpectedly contained claimable work")
			}
			serverAllowed := serverErr == nil

			var out, errOut bytes.Buffer
			code := policyMain([]string{"simulate", "review", "-policy", policyPath, "-db", dbPath, "-repo", tt.repo, "-item", "13"}, &out, &errOut)
			if code != 0 {
				t.Fatalf("simulate code=%d stderr=%s", code, errOut.String())
			}
			paused := tt.globalPause || tt.projectPause
			sharedDecision := policy.DecideReview(effective, tt.repo, paused, tt.used)
			cliAllowed := strings.Contains(out.String(), "Decision: ALLOW —")
			if sharedDecision.Allowed != tt.allowed || serverAllowed != tt.allowed || cliAllowed != tt.allowed {
				t.Fatalf("shared=%+v serverErr=%v CLI=%q; want allowed=%t", sharedDecision, serverErr, out.String(), tt.allowed)
			}
			if !strings.Contains(out.String(), "Decision: "+decisionLabel(sharedDecision.Allowed)+" — "+sharedDecision.Reason) {
				t.Fatalf("CLI reason disagrees with policy decision: %+v output=%q", sharedDecision, out.String())
			}
			if fakeGH.CallCount() != 0 {
				t.Fatalf("policy simulation contacted GitHub %d times", fakeGH.CallCount())
			}
		})
	}
}

func TestPolicyExplainShowsOverridesPausesAndReviewLimit(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.yaml")
	dbPath := filepath.Join(dir, "rusui.db")
	raw := []byte(`version: 2
defaults:
  session_kinds: [review, run]
  egress: trusted
  review: false
  comments: false
  max_reviews_per_repo_per_utc_day: 8
projects:
  test:
    budgets:
      max_concurrent_leases: 3
    repos:
      example/repo:
        review: true
`)
	if err := os.WriteFile(policyPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	stateStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := stateStore.Tx(func(tx *sql.Tx) error {
		if err := store.OverlaySet(tx, "pause:test", "1"); err != nil {
			return err
		}
		return store.IncrReviewsToday(tx, "example/repo", now.Format("2006-01-02"))
	}); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := policyMain([]string{"explain", "-policy", policyPath, "-db", dbPath, "-repo", "example/repo"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("explain code=%d stderr=%s", code, errOut.String())
	}
	for _, want := range []string{
		"review: true (repository override)",
		"comments: false (defaults)",
		"session_kinds: review, run (defaults)",
		"budgets.max_concurrent_leases: 3 (project)",
		"project pause: true",
		"1/8 used, 7 remaining",
		"Review decision: REFUSE — project is paused",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("explanation missing %q:\n%s", want, out.String())
		}
	}
}

func TestPolicyRuntimeDatabaseIsReadOnly(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "rusui.db")
	stateStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Tx(func(tx *sql.Tx) error {
		return store.OverlaySet(tx, "pause:test", "0")
	}); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}
	readOnly, err := openPolicyStateDBReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readOnly.Close() }()
	if _, err := readOnly.Exec(`UPDATE overlay SET value='1' WHERE key='pause:test'`); err == nil {
		t.Fatal("read-only policy database accepted a write")
	}
}

func TestPolicySimulationWithMissingDatabaseDoesNotCreateIt(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.yaml")
	dbPath := filepath.Join(dir, "missing.db")
	if err := os.WriteFile(policyPath, policy.StarterYAML(), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := policyMain([]string{"simulate", "review", "-policy", policyPath, "-db", dbPath, "-repo", "Sannrox/rusui", "-item", "13"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("simulate code=%d stderr=%s", code, errOut.String())
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("simulation created or unexpectedly found database: %v", err)
	}
	if !strings.Contains(out.String(), "not found; no stored pauses or daily counts") {
		t.Fatalf("missing database state not disclosed: %s", out.String())
	}
}

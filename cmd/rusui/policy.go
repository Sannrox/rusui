package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
	_ "modernc.org/sqlite"
)

type policyRuntimeState struct {
	DatabaseFound bool
	GlobalPaused  bool
	ProjectPaused bool
	ReviewsToday  int
	Day           string
}

func policyCLI(args []string) {
	if code := policyMain(args, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

func policyMain(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		policyUsage(errOut)
		return 2
	}
	switch args[0] {
	case "init":
		return policyInit(args[1:], out, errOut)
	case "explain":
		return policyExplain(args[1:], out, errOut)
	case "simulate":
		return policySimulate(args[1:], out, errOut)
	default:
		policyUsage(errOut)
		return 2
	}
}

func policyUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage: rusui policy init [-file policy.yaml]")
	_, _ = fmt.Fprintln(w, "       rusui policy explain -repo OWNER/REPO [-policy policy.yaml] [-db rusui.db]")
	_, _ = fmt.Fprintln(w, "       rusui policy simulate review -repo OWNER/REPO -item NUMBER [-policy policy.yaml] [-db rusui.db]")
}

func policyInit(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("policy init", flag.ContinueOnError)
	fs.SetOutput(errOut)
	path := fs.String("file", "policy.yaml", "new policy file")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		policyUsage(errOut)
		return 2
	}
	f, err := os.OpenFile(*path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "policy init: %v\n", err)
		return 1
	}
	if _, err = f.Write(policy.StarterYAML()); err != nil {
		_ = f.Close()
		_ = os.Remove(*path)
		_, _ = fmt.Fprintf(errOut, "policy init: write %s: %v\n", *path, err)
		return 1
	}
	if err = f.Close(); err != nil {
		_ = os.Remove(*path)
		_, _ = fmt.Fprintf(errOut, "policy init: close %s: %v\n", *path, err)
		return 1
	}
	_, _ = fmt.Fprintf(out, "created %s\n", *path)
	return 0
}

func policyExplain(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("policy explain", flag.ContinueOnError)
	fs.SetOutput(errOut)
	policyPath := fs.String("policy", "policy.yaml", "policy file")
	dbPath := fs.String("db", "rusui.db", "SQLite database to inspect read-only")
	repo := fs.String("repo", "", "bound repository as OWNER/REPO")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		policyUsage(errOut)
		return 2
	}
	if *repo == "" {
		_, _ = fmt.Fprintln(errOut, "policy explain: -repo is required")
		return 2
	}
	effective, err := policy.Load(*policyPath)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "policy explain: %v\n", err)
		return 1
	}
	explanation, bound, err := effective.ExplainRepo(*repo)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "policy explain: %v\n", err)
		return 1
	}
	state, err := readPolicyRuntimeState(*dbPath, explanation.Project.Slug, *repo, time.Now().UTC())
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "policy explain: read runtime state: %v\n", err)
		return 1
	}
	printPolicyExplanation(out, effective, explanation, bound, state, *dbPath)
	return 0
}

func policySimulate(args []string, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] != "review" {
		policyUsage(errOut)
		return 2
	}
	fs := flag.NewFlagSet("policy simulate review", flag.ContinueOnError)
	fs.SetOutput(errOut)
	policyPath := fs.String("policy", "policy.yaml", "policy file")
	dbPath := fs.String("db", "rusui.db", "SQLite database to inspect read-only")
	repo := fs.String("repo", "", "bound repository as OWNER/REPO")
	item := fs.Int("item", 0, "pull request number")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		policyUsage(errOut)
		return 2
	}
	if *repo == "" || *item <= 0 {
		_, _ = fmt.Fprintln(errOut, "policy simulate review: -repo and a positive -item are required")
		return 2
	}
	effective, err := policy.Load(*policyPath)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "policy simulate review: %v\n", err)
		return 1
	}
	explanation, bound, err := effective.ExplainRepo(*repo)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "policy simulate review: %v\n", err)
		return 1
	}
	state, err := readPolicyRuntimeState(*dbPath, explanation.Project.Slug, *repo, time.Now().UTC())
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "policy simulate review: read runtime state: %v\n", err)
		return 1
	}
	decision := policy.DecideReview(effective, *repo, state.GlobalPaused || state.ProjectPaused, state.ReviewsToday)
	printPolicyDecision(out, *repo, *item, explanation, bound, state, *dbPath, decision)
	return 0
}

func readPolicyRuntimeState(dbPath, project, repo string, now time.Time) (policyRuntimeState, error) {
	state := policyRuntimeState{Day: now.UTC().Format("2006-01-02")}
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state, nil
		}
		return state, err
	}
	db, err := openPolicyStateDBReadOnly(dbPath)
	if err != nil {
		return state, err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.Begin()
	if err != nil {
		return state, err
	}
	defer func() { _ = tx.Rollback() }()
	global, err := store.OverlayGet(tx, "pause:global")
	if err != nil {
		return state, err
	}
	projectPause := ""
	if project != "" {
		projectPause, err = store.OverlayGet(tx, "pause:"+project)
		if err != nil {
			return state, err
		}
	}
	state.GlobalPaused = global == "1"
	state.ProjectPaused = projectPause == "1"
	err = tx.QueryRow(`SELECT count FROM daily_review_counts WHERE repo=? AND day=?`, repo, state.Day).Scan(&state.ReviewsToday)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return state, err
	}
	if err := tx.Commit(); err != nil {
		return state, err
	}
	state.DatabaseFound = true
	return state, nil
}

func openPolicyStateDBReadOnly(dbPath string) (*sql.DB, error) {
	path, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, err
	}
	fileURL := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	db, err := sql.Open("sqlite", fileURL.String()+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func printPolicyExplanation(out io.Writer, effective *policy.Effective, explanation policy.RepoExplanation, bound bool, state policyRuntimeState, dbPath string) {
	_, _ = fmt.Fprintf(out, "Policy revision: %s\nRepository: %s\n", effective.Hash, explanation.Repository)
	if !bound {
		_, _ = fmt.Fprintln(out, "  status: not bound by this policy")
		decision := policy.DecideReview(effective, explanation.Repository, state.GlobalPaused || state.ProjectPaused, state.ReviewsToday)
		_, _ = fmt.Fprintf(out, "  review decision: %s — %s\n", decisionLabel(decision.Allowed), decision.Reason)
		printPolicyRuntimeState(out, state, dbPath, -1)
		return
	}
	_, _ = fmt.Fprintf(out, "Project: %s\n", explanation.Project.Slug)
	_, _ = fmt.Fprintln(out, "Repository policy:")
	printPolicyField(out, "visibility", explanation.Repo.Visibility, explanation.Sources["visibility"])
	printPolicyField(out, "review", fmt.Sprint(explanation.Repo.Review), explanation.Sources["review"])
	printPolicyField(out, "comments", fmt.Sprint(explanation.Repo.Comments), explanation.Sources["comments"])
	printPolicyField(out, "close", fmt.Sprint(explanation.Repo.Close), explanation.Sources["close"])
	printPolicyField(out, "implement", fmt.Sprint(explanation.Repo.Implement), explanation.Sources["implement"])
	printPolicyField(out, "land", fmt.Sprint(explanation.Repo.Land), explanation.Sources["land"])
	printPolicyField(out, "never_release", fmt.Sprint(explanation.Repo.NeverRelease), explanation.Sources["never_release"])
	printPolicyField(out, "never_leak_private_to_public", fmt.Sprint(explanation.Repo.NeverLeakPrivateToPublic), explanation.Sources["never_leak_private_to_public"])
	printPolicyField(out, "max_reviews_per_repo_per_utc_day", fmt.Sprint(explanation.Repo.MaxReviewsPerRepoPerUTCDay), explanation.Sources["max_reviews_per_repo_per_utc_day"])
	_, _ = fmt.Fprintln(out, "Project policy:")
	printPolicyField(out, "session_kinds", strings.Join(explanation.Project.SessionKinds, ", "), explanation.Sources["session_kinds"])
	printPolicyField(out, "egress", explanation.Project.Egress, explanation.Sources["egress"])
	printPolicyField(out, "budgets.max_concurrent_leases", fmt.Sprint(explanation.Project.MaxConcurrentLeases()), explanation.Sources["budgets.max_concurrent_leases"])
	printPolicyField(out, "permissions", fmt.Sprintf("%d rules", len(explanation.Project.Permissions)), explanation.Sources["permissions"])
	for _, rule := range explanation.Project.Permissions {
		action := rule.Action
		if action == "" {
			action = "allow"
		}
		_, _ = fmt.Fprintf(out, "    tool=%q kind=%q command=%q action=%q\n", rule.Tool, rule.Kind, rule.Command, action)
	}
	localRuntime := "disabled"
	if explanation.Project.LocalRuntime != nil {
		localRuntime = fmt.Sprintf("argv=%q cwd=%q", explanation.Project.LocalRuntime.Argv, explanation.Project.LocalRuntime.Cwd)
	}
	printPolicyField(out, "local_runtime", localRuntime, explanation.Sources["local_runtime"])
	printPolicyRuntimeState(out, state, dbPath, explanation.Repo.MaxReviewsPerRepoPerUTCDay)
	decision := policy.DecideReview(effective, explanation.Repository, state.GlobalPaused || state.ProjectPaused, state.ReviewsToday)
	_, _ = fmt.Fprintf(out, "Review decision: %s — %s\n", decisionLabel(decision.Allowed), decision.Reason)
}

func printPolicyRuntimeState(out io.Writer, state policyRuntimeState, dbPath string, reviewLimit int) {
	if state.DatabaseFound {
		_, _ = fmt.Fprintf(out, "Runtime state: read-only database %s\n", dbPath)
	} else {
		_, _ = fmt.Fprintf(out, "Runtime state: %s not found; no stored pauses or daily counts\n", dbPath)
	}
	_, _ = fmt.Fprintf(out, "  global pause: %t\n  project pause: %t\n", state.GlobalPaused, state.ProjectPaused)
	if reviewLimit >= 0 {
		remaining := max(reviewLimit-state.ReviewsToday, 0)
		_, _ = fmt.Fprintf(out, "  reviews today (%s UTC): %d/%d used, %d remaining\n", state.Day, state.ReviewsToday, reviewLimit, remaining)
	}
}

func printPolicyDecision(out io.Writer, repo string, item int, explanation policy.RepoExplanation, bound bool, state policyRuntimeState, dbPath string, decision policy.ReviewDecision) {
	_, _ = fmt.Fprintf(out, "Review event: %s#%d\n", repo, item)
	_, _ = fmt.Fprintf(out, "Decision: %s — %s\n", decisionLabel(decision.Allowed), decision.Reason)
	if bound {
		_, _ = fmt.Fprintf(out, "Project: %s\n", explanation.Project.Slug)
	}
	reviewLimit := -1
	if bound {
		reviewLimit = explanation.Repo.MaxReviewsPerRepoPerUTCDay
	}
	printPolicyRuntimeState(out, state, dbPath, reviewLimit)
	_, _ = fmt.Fprintln(out, "Scope: prospective review policy only; queue, duplicate state, and live GitHub data are not inspected.")
}

func printPolicyField(out io.Writer, name, value, source string) {
	_, _ = fmt.Fprintf(out, "  %s: %s (%s)\n", name, value, source)
}

func decisionLabel(allowed bool) string {
	if allowed {
		return "ALLOW"
	}
	return "REFUSE"
}

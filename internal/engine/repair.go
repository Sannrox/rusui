package engine

import (
	"database/sql"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

const (
	RepairLabel     = "rusui:repair"
	repairCooldown  = 7 * 24 * time.Hour
	repairEffortFmt = "repair:%s#%d"
)

var ErrRepairRecheck = errors.New("repair recheck refused")

// DocsAllowedPaths is the first repair category: documentation only (ADR 0038 D7).
func DocsAllowedPaths() []string {
	return []string{
		"docs",
		"README.md", "ARCHITECTURE.md", "AGENTS.md", "CONTRIBUTING.md",
		"ROADMAP.md", "SECURITY.md", "CONTEXT.md", "VISION.md",
		"CHANGELOG.md", "CODE_OF_CONDUCT.md",
	}
}

func RepairEffortKey(repo string, item int) string {
	return fmt.Sprintf(repairEffortFmt, repo, item)
}

func parseRepairEffort(key string) (repo string, item int, ok bool) {
	rest, found := strings.CutPrefix(key, "repair:")
	if !found {
		return "", 0, false
	}
	repo, n, found := strings.Cut(rest, "#")
	if !found || repo == "" {
		return "", 0, false
	}
	item, err := strconv.Atoi(n)
	if err != nil || item <= 0 {
		return "", 0, false
	}
	return repo, item, true
}

// RepairEligible is the item+policy half of ADR 0038 D2/D3/D7. Competing
// effort, cooldown, and budget are checked at admit and again before publish.
func RepairEligible(it snapshot.Item, r policy.Repo) (ok bool, reason string) {
	if it.ItemKind != "issue" || it.State != "open" {
		return false, "not_open_issue"
	}
	if it.Draft {
		return false, "draft"
	}
	if !hasLabel(it, RepairLabel) {
		return false, "not_labeled"
	}
	if r.Repair != policy.RepairDocs {
		return false, "repair_off"
	}
	if !r.Implement {
		return false, "implement_off"
	}
	for _, l := range it.Labels {
		if slices.Contains(r.ProtectedLabels, l) {
			return false, "protected_label"
		}
	}
	if humanAssigned(it) {
		return false, "assigned"
	}
	return true, ""
}

func hasLabel(it snapshot.Item, name string) bool {
	return slices.Contains(it.Labels, name)
}

func humanAssigned(it snapshot.Item) bool {
	for _, a := range it.Assignees {
		if !strings.HasSuffix(strings.ToLower(a), "[bot]") {
			return true
		}
	}
	return false
}

func competingBranch(heads []string, item int, exclude []string) bool {
	n := strconv.Itoa(item)
	for _, h := range heads {
		h = strings.TrimPrefix(h, "refs/heads/")
		if slices.Contains(exclude, h) {
			continue
		}
		base := path.Base(h)
		if base == n || strings.HasPrefix(base, n+"-") {
			return true
		}
	}
	return false
}

func prLinksItem(pr snapshot.Item, item int) bool {
	if pr.ItemKind != "pull" || pr.State != "open" {
		return false
	}
	if slices.Contains(pr.LinkedSameRepoItems, item) {
		return true
	}
	return slices.Contains(gh.LinkedSameRepo(pr.Body, pr.Repo, pr.Item), item)
}

func (e *Engine) maybeAdmitRepair(it snapshot.Item) {
	if it.ItemKind != "issue" || it.State != "open" || !hasLabel(it, RepairLabel) {
		return
	}
	pol := e.PolicySnapshot()
	r, ok := pol.Repo(it.Repo)
	if !ok {
		return
	}
	key := RepairEffortKey(it.Repo, it.Item)
	if existing, err := store.GetLatestTask(e.Store, key); err != nil {
		e.exception(fmt.Sprintf("repair %s#%d: %v", it.Repo, it.Item, err))
		return
	} else if existing != nil && taskStillBound(existing.State) {
		return
	}
	hash := snapshot.ItemHash(it)
	if ok, reason := RepairEligible(it, r); !ok {
		e.recordRepair(it, hash, store.RepairDeclined, reason)
		return
	}
	if reason, err := e.repairCompeting(it, nil, 0); err != nil {
		e.recordRepair(it, hash, store.RepairDeclined, "competing_unavailable")
		e.exception(fmt.Sprintf("repair %s#%d: %v", it.Repo, it.Item, err))
		return
	} else if reason != "" {
		e.recordRepair(it, hash, store.RepairDeclined, reason)
		return
	}
	blocked := ""
	var selectedID int64
	err := e.Store.Tx(func(tx *sql.Tx) error {
		paused, err := e.repoPaused(tx, it.Repo)
		if err != nil {
			return err
		}
		if paused {
			blocked = "paused"
			_, err = e.insertRepairTx(tx, it, hash, store.RepairDeclined, blocked)
			return err
		}
		declinedAt, ok, err := store.LatestDeclineTx(tx, it.Repo, it.Item, hash)
		if err != nil {
			return err
		}
		if ok {
			t, err := time.Parse(time.RFC3339Nano, declinedAt)
			if err != nil {
				t, err = time.Parse(time.RFC3339, declinedAt)
			}
			if err == nil && e.now().Sub(t) < repairCooldown {
				blocked = "cooldown"
				return nil
			}
		}
		latest, ok, err := store.LatestTaskTx(tx, key)
		if err != nil {
			return err
		}
		if ok && taskStillBound(latest.State) {
			return nil
		}
		id, selHash, hasSelected, err := store.LatestSelectedRowTx(tx, it.Repo, it.Item)
		if err != nil {
			return err
		}
		if hasSelected && selHash == hash {
			selectedID = id
			return nil
		}
		n, err := store.CountRepairsTodayTx(tx, it.Repo, e.now().Format("2006-01-02"))
		if err != nil {
			return err
		}
		if n >= r.MaxRepairsPerRepoPerUTCDay {
			blocked = "budget"
			_, err = e.insertRepairTx(tx, it, hash, store.RepairDeclined, blocked)
			return err
		}
		selectedID, err = e.insertRepairTx(tx, it, hash, store.RepairSelected, "docs")
		return err
	})
	if err != nil {
		e.exception(fmt.Sprintf("repair %s#%d: %v", it.Repo, it.Item, err))
		return
	}
	if blocked != "" || selectedID == 0 {
		return
	}
	prompt := strings.TrimSpace(it.Title)
	if it.Body != "" {
		prompt = prompt + "\n\n" + it.Body
	}
	if prompt == "" {
		prompt = fmt.Sprintf("documentation correction for %s#%d", it.Repo, it.Item)
	}
	if _, err = e.StartTask(r.Project, TaskSpec{
		EffortKey:    key,
		Prompt:       prompt,
		Repo:         it.Repo,
		Ref:          it.DefaultBranch,
		BaseSHA:      it.MainSHA,
		AllowedPaths: DocsAllowedPaths(),
	}); err != nil {
		e.exception(fmt.Sprintf("repair %s#%d start: %v", it.Repo, it.Item, err))
		e.retractRepairSelection(it, hash, selectedID)
	}
}

func (e *Engine) repairCompeting(it snapshot.Item, excludeBranches []string, excludePR int) (string, error) {
	heads, err := e.GitHub.ListHeads(it.Repo)
	if err != nil {
		return "", err
	}
	if competingBranch(heads, it.Item, excludeBranches) {
		return "competing_branch", nil
	}
	open, err := e.GitHub.ListOpenItems(it.Repo)
	if err != nil {
		return "", err
	}
	for _, other := range open {
		if other.Item == excludePR {
			continue
		}
		if prLinksItem(other, it.Item) {
			return "competing_pr", nil
		}
	}
	return "", nil
}

func (e *Engine) recordRepair(it snapshot.Item, hash, decision, reason string) {
	err := e.Store.Tx(func(tx *sql.Tx) error {
		_, err := e.insertRepairTx(tx, it, hash, decision, reason)
		return err
	})
	if err != nil {
		e.exception(fmt.Sprintf("repair %s#%d record: %v", it.Repo, it.Item, err))
	}
}

func (e *Engine) insertRepairTx(tx *sql.Tx, it snapshot.Item, hash, decision, reason string) (int64, error) {
	return store.InsertRepairDecisionTx(tx, store.RepairDecision{
		Repo: it.Repo, Item: it.Item, ItemHash: hash,
		Decision: decision, Reason: reason,
		CreatedAt: e.now().Format(time.RFC3339Nano),
	})
}

func (e *Engine) retractRepairSelection(it snapshot.Item, hash string, selectedID int64) {
	err := e.Store.Tx(func(tx *sql.Tx) error {
		if err := store.DeleteRepairDecisionTx(tx, selectedID); err != nil {
			return err
		}
		_, err := e.insertRepairTx(tx, it, hash, store.RepairDeclined, "start_failed")
		return err
	})
	if err != nil {
		e.exception(fmt.Sprintf("repair %s#%d retract: %v", it.Repo, it.Item, err))
	}
}

func (e *Engine) recheckRepair(task *store.Task, ask WriteProposal) error {
	repo, item, ok := parseRepairEffort(task.EffortKey)
	if !ok {
		return nil
	}
	it, err := e.GitHub.GetItem(repo, item, "issue")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRepairRecheck, err)
	}
	pol := e.PolicySnapshot()
	r, bound := pol.Repo(repo)
	if !bound {
		return fmt.Errorf("%w: repo not bound", ErrRepairRecheck)
	}
	if ok, reason := RepairEligible(it, r); !ok {
		return fmt.Errorf("%w: %s", ErrRepairRecheck, reason)
	}
	selected, ok, err := store.LatestSelected(e.Store, repo, item)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: no selected decision", ErrRepairRecheck)
	}
	if selected != snapshot.ItemHash(it) {
		return fmt.Errorf("%w: stale", ErrRepairRecheck)
	}
	if existing, err := store.GetLatestTask(e.Store, task.EffortKey); err != nil {
		return err
	} else if existing != nil && taskStillBound(existing.State) && existing.SessionID != task.SessionID {
		return fmt.Errorf("%w: competing_task", ErrRepairRecheck)
	}
	if it.MainSHA != "" && task.BaseSHA != "" && it.MainSHA != task.BaseSHA {
		return fmt.Errorf("%w: %w", ErrRepairRecheck, ErrStaleSource)
	}
	paused := false
	if err := e.Store.Tx(func(tx *sql.Tx) error {
		var err error
		paused, err = e.repoPaused(tx, repo)
		return err
	}); err != nil {
		return err
	}
	if paused {
		return fmt.Errorf("%w: paused", ErrRepairRecheck)
	}
	excludePR := 0
	if prev, ok, err := SessionPublication(e.Store, task.SessionID); err != nil {
		return err
	} else if ok {
		excludePR = prev.PullRequest
	}
	if reason, err := e.repairCompeting(it, []string{ask.Branch}, excludePR); err != nil {
		return fmt.Errorf("%w: %w", ErrRepairRecheck, err)
	} else if reason != "" {
		return fmt.Errorf("%w: %s", ErrRepairRecheck, reason)
	}
	return nil
}

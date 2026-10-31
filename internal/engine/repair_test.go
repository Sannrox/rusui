package engine_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

const repairFixture = `version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
  review: true
  comments: false
  close: false
  implement: true
  land: false
  repair: docs
  max_reviews_per_repo_per_utc_day: 50
  max_repairs_per_repo_per_utc_day: 1
projects:
  test:
    repos:
      example/test-repo:
        visibility: public
        review: true
        implement: true
        repair: docs
`

func setupRepair(t *testing.T) *harn {
	t.Helper()
	h := setup(t)
	pol, err := policy.Parse([]byte(repairFixture))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(pol)
	return h
}

func repairIssue(n int) snapshot.Item {
	it := issue(n)
	it.Labels = []string{engine.RepairLabel}
	it.Title = "docs typo"
	it.Body = "fix README.md"
	return it
}

func latestRepair(t *testing.T, h *harn, item int) (decision, reason string) {
	t.Helper()
	err := h.st.DB.QueryRow(`SELECT decision, reason FROM repair_decisions WHERE repo=? AND item=? ORDER BY id DESC LIMIT 1`,
		"example/test-repo", item).Scan(&decision, &reason)
	if err != nil {
		t.Fatal(err)
	}
	return decision, reason
}

func repairDecisionCount(t *testing.T, h *harn, item int) int {
	t.Helper()
	var n int
	if err := h.st.DB.QueryRow(`SELECT COUNT(*) FROM repair_decisions WHERE repo=? AND item=?`,
		"example/test-repo", item).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRepairAdmitsLabeledDocsIssue(t *testing.T) {
	h := setupRepair(t)
	h.putRefresh(repairIssue(1))
	task, err := store.GetLatestTask(h.st, engine.RepairEffortKey("example/test-repo", 1))
	if err != nil || task == nil || task.State != "open" {
		t.Fatalf("task %+v %v", task, err)
	}
	var paths []string
	if err := json.Unmarshal([]byte(task.AllowedPaths), &paths); err != nil || len(paths) == 0 || paths[0] != "docs" {
		t.Fatalf("paths %s %v", task.AllowedPaths, err)
	}
	if d, r := latestRepair(t, h, 1); d != store.RepairSelected || r != "docs" {
		t.Fatalf("decision %s %s", d, r)
	}
}

func TestRepairDefaultOff(t *testing.T) {
	h := setup(t)
	h.putRefresh(repairIssue(1))
	task, err := store.GetLatestTask(h.st, engine.RepairEffortKey("example/test-repo", 1))
	if err != nil || task != nil {
		t.Fatalf("task %+v %v", task, err)
	}
	if d, r := latestRepair(t, h, 1); d != store.RepairDeclined || r != "repair_off" {
		t.Fatalf("decision %s %s", d, r)
	}
}

func TestRepairAssignedDeclines(t *testing.T) {
	h := setupRepair(t)
	it := repairIssue(1)
	it.Assignees = []string{"alice"}
	h.putRefresh(it)
	if d, r := latestRepair(t, h, 1); d != store.RepairDeclined || r != "assigned" {
		t.Fatalf("decision %s %s", d, r)
	}
}

func TestRepairBotAssigneeStillAdmits(t *testing.T) {
	h := setupRepair(t)
	it := repairIssue(1)
	it.Assignees = []string{"rusui[bot]"}
	h.putRefresh(it)
	task, err := store.GetLatestTask(h.st, engine.RepairEffortKey("example/test-repo", 1))
	if err != nil || task == nil {
		t.Fatalf("task %+v %v", task, err)
	}
}

func TestRepairProtectedLabelDeclines(t *testing.T) {
	h := setupRepair(t)
	it := repairIssue(1)
	it.Labels = []string{engine.RepairLabel, "security"}
	h.putRefresh(it)
	if d, r := latestRepair(t, h, 1); d != store.RepairDeclined || r != "protected_label" {
		t.Fatalf("decision %s %s", d, r)
	}
}

func TestRepairCompetingBranchDeclines(t *testing.T) {
	h := setupRepair(t)
	h.f.Heads = []string{"feat/1"}
	h.putRefresh(repairIssue(1))
	if d, r := latestRepair(t, h, 1); d != store.RepairDeclined || r != "competing_branch" {
		t.Fatalf("decision %s %s", d, r)
	}
}

func TestRepairCompetingPRDeclines(t *testing.T) {
	h := setupRepair(t)
	h.f.Put(snapshot.Item{
		Repo: "example/test-repo", Item: 9, ItemKind: "pull", State: "open",
		Body: "Fixes #1", LinkedSameRepoItems: []int{1},
	})
	h.putRefresh(repairIssue(1))
	if d, r := latestRepair(t, h, 1); d != store.RepairDeclined || r != "competing_pr" {
		t.Fatalf("decision %s %s", d, r)
	}
}

func TestRepairCompetingPRQualifiedLink(t *testing.T) {
	h := setupRepair(t)
	h.f.Put(snapshot.Item{
		Repo: "example/test-repo", Item: 9, ItemKind: "pull", State: "open",
		Body: "Fixes example/test-repo#1 and https://github.com/example/test-repo/issues/1",
	})
	h.putRefresh(repairIssue(1))
	if d, r := latestRepair(t, h, 1); d != store.RepairDeclined || r != "competing_pr" {
		t.Fatalf("decision %s %s", d, r)
	}
}

func TestRepairBudgetDeclinesSecond(t *testing.T) {
	h := setupRepair(t)
	h.putRefresh(repairIssue(1))
	if d, _ := latestRepair(t, h, 1); d != store.RepairSelected {
		t.Fatalf("first %s", d)
	}
	h.putRefresh(repairIssue(2))
	if d, r := latestRepair(t, h, 2); d != store.RepairDeclined || r != "budget" {
		t.Fatalf("second %s %s", d, r)
	}
}

func TestRepairCooldownUntilHashChanges(t *testing.T) {
	h := setupRepair(t)
	h.f.Heads = []string{"feat/1"}
	it := repairIssue(1)
	h.putRefresh(it)
	if d, r := latestRepair(t, h, 1); d != store.RepairDeclined || r != "competing_branch" {
		t.Fatalf("first %s %s", d, r)
	}
	h.f.Heads = nil
	h.putRefresh(it)
	if d, r := latestRepair(t, h, 1); d != store.RepairDeclined || r != "competing_branch" {
		t.Fatalf("window keeps first decline %s %s", d, r)
	}
	if n := repairDecisionCount(t, h, 1); n != 1 {
		t.Fatalf("refresh must not restart cooldown, got %d rows", n)
	}
	task, err := store.GetLatestTask(h.st, engine.RepairEffortKey("example/test-repo", 1))
	if err != nil || task != nil {
		t.Fatalf("task during cooldown %+v %v", task, err)
	}
	it.Title = "docs typo revised"
	h.putRefresh(it)
	if d, r := latestRepair(t, h, 1); d != store.RepairSelected || r != "docs" {
		t.Fatalf("hash change %s %s", d, r)
	}
}

func TestRepairEligiblePure(t *testing.T) {
	r := policy.Repo{Implement: true, Repair: policy.RepairDocs, ProtectedLabels: []string{policy.LabelHold, policy.LabelSecurity}}
	it := repairIssue(1)
	if ok, reason := engine.RepairEligible(it, r); !ok {
		t.Fatalf("want eligible, got %s", reason)
	}
	it.State = "closed"
	if ok, reason := engine.RepairEligible(it, r); ok || reason != "not_open_issue" {
		t.Fatalf("closed %v %s", ok, reason)
	}
}

func startRepairTask(t *testing.T, h *harn, it snapshot.Item) *store.Task {
	t.Helper()
	h.f.Put(it)
	task, err := h.e.StartTask("test", engine.TaskSpec{
		EffortKey:    engine.RepairEffortKey(it.Repo, it.Item),
		Prompt:       it.Title,
		Repo:         it.Repo,
		Ref:          "main",
		BaseSHA:      "aaa",
		AllowedPaths: engine.DocsAllowedPaths(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.st.Tx(func(tx *sql.Tx) error {
		_, err := store.InsertRepairDecisionTx(tx, store.RepairDecision{
			Repo: it.Repo, Item: it.Item, ItemHash: snapshot.ItemHash(it),
			Decision: store.RepairSelected, Reason: "docs",
			CreatedAt: h.clk.T.Format(time.RFC3339Nano),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestRepairPublicationRecheckRefusesAssigned(t *testing.T) {
	h := setupRepair(t)
	pub := &fakePublisher{}
	h.e.Publisher = pub
	it := repairIssue(1)
	task := startRepairTask(t, h, it)
	assigned := it
	assigned.Assignees = []string{"alice"}
	h.f.Put(assigned)
	branch := fmt.Sprintf("rusui/%d/pin", task.SessionID)
	r := completePublish(t, h, h.claim(), branch, "sha-a")
	if r.Outcome != engine.OutcomeProposed {
		t.Fatalf("propose %+v", r)
	}
	if _, err := h.e.ApplySessionWrite(task.SessionID); !errors.Is(err, engine.ErrRepairRecheck) {
		t.Fatalf("apply %v", err)
	}
	if len(pub.specs) != 0 {
		t.Fatalf("wrote %+v", pub.specs)
	}
}

func TestRepairPublicationRecheckRequiresSelected(t *testing.T) {
	h := setupRepair(t)
	h.e.Publisher = &fakePublisher{}
	it := repairIssue(1)
	h.f.Put(it)
	task, err := h.e.StartTask("test", engine.TaskSpec{
		EffortKey:    engine.RepairEffortKey(it.Repo, it.Item),
		Prompt:       it.Title,
		Repo:         it.Repo,
		Ref:          "main",
		BaseSHA:      "aaa",
		AllowedPaths: engine.DocsAllowedPaths(),
	})
	if err != nil {
		t.Fatal(err)
	}
	branch := fmt.Sprintf("rusui/%d/pin", task.SessionID)
	completePublish(t, h, h.claim(), branch, "sha-a")
	if _, err := h.e.ApplySessionWrite(task.SessionID); !errors.Is(err, engine.ErrRepairRecheck) {
		t.Fatalf("apply %v", err)
	}
}

func TestRepairPublicationRecheckStaleHash(t *testing.T) {
	h := setupRepair(t)
	h.e.Publisher = &fakePublisher{}
	it := repairIssue(1)
	task := startRepairTask(t, h, it)
	stale := it
	stale.Title = "docs typo revised"
	h.f.Put(stale)
	branch := fmt.Sprintf("rusui/%d/pin", task.SessionID)
	completePublish(t, h, h.claim(), branch, "sha-a")
	if _, err := h.e.ApplySessionWrite(task.SessionID); !errors.Is(err, engine.ErrRepairRecheck) {
		t.Fatalf("apply %v", err)
	}
}

func TestRepairDecisionsPersist(t *testing.T) {
	h := setupRepair(t)
	h.putRefresh(repairIssue(1))
	var n int
	if err := h.st.DB.QueryRow(`SELECT COUNT(*) FROM repair_decisions`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("count %d %v", n, err)
	}
	if err := h.st.DB.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='repair_decisions'`).Scan(&n); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
}

func TestRepairCooldownClock(t *testing.T) {
	h := setupRepair(t)
	h.f.Heads = []string{"feat/1"}
	it := repairIssue(1)
	h.putRefresh(it)
	h.f.Heads = nil
	h.putRefresh(it)
	if n := repairDecisionCount(t, h, 1); n != 1 {
		t.Fatalf("refresh inside window wrote %d rows", n)
	}
	h.clk.T = h.clk.T.Add(8 * 24 * time.Hour)
	h.putRefresh(it)
	if d, r := latestRepair(t, h, 1); d != store.RepairSelected || r != "docs" {
		t.Fatalf("after cooldown %s %s", d, r)
	}
}

func TestRepairOffThenEnableSelects(t *testing.T) {
	h := setup(t)
	it := repairIssue(1)
	h.putRefresh(it)
	if d, r := latestRepair(t, h, 1); d != store.RepairDeclined || r != "repair_off" {
		t.Fatalf("off %s %s", d, r)
	}
	pol, err := policy.Parse([]byte(repairFixture))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(pol)
	h.putRefresh(it)
	if d, r := latestRepair(t, h, 1); d != store.RepairSelected || r != "docs" {
		t.Fatalf("enable %s %s", d, r)
	}
}

func TestRepairBudgetRetriesNextUTCDay(t *testing.T) {
	h := setupRepair(t)
	h.putRefresh(repairIssue(1))
	h.putRefresh(repairIssue(2))
	if d, r := latestRepair(t, h, 2); d != store.RepairDeclined || r != "budget" {
		t.Fatalf("budget %s %s", d, r)
	}
	h.clk.T = h.clk.T.Add(24 * time.Hour)
	h.putRefresh(repairIssue(2))
	if d, r := latestRepair(t, h, 2); d != store.RepairSelected || r != "docs" {
		t.Fatalf("next day %s %s", d, r)
	}
}

func TestRepairStartFailureDoesNotConsumeBudget(t *testing.T) {
	h := setupRepair(t)
	bad := repairIssue(1)
	bad.MainSHA = ""
	h.putRefresh(bad)
	if d, r := latestRepair(t, h, 1); d != store.RepairDeclined || r != "start_failed" {
		t.Fatalf("start %s %s", d, r)
	}
	task, err := store.GetLatestTask(h.st, engine.RepairEffortKey("example/test-repo", 1))
	if err != nil || task != nil {
		t.Fatalf("task %+v %v", task, err)
	}
	h.putRefresh(repairIssue(2))
	if d, r := latestRepair(t, h, 2); d != store.RepairSelected || r != "docs" {
		t.Fatalf("budget after start fail %s %s", d, r)
	}
}

func TestRepairRetriesOrphanSelected(t *testing.T) {
	h := setupRepair(t)
	it := repairIssue(1)
	if err := h.st.Tx(func(tx *sql.Tx) error {
		_, err := store.InsertRepairDecisionTx(tx, store.RepairDecision{
			Repo: it.Repo, Item: it.Item, ItemHash: snapshot.ItemHash(it),
			Decision: store.RepairSelected, Reason: "docs",
			CreatedAt: h.clk.T.Format(time.RFC3339Nano),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	h.putRefresh(it)
	task, err := store.GetLatestTask(h.st, engine.RepairEffortKey(it.Repo, it.Item))
	if err != nil || task == nil {
		t.Fatalf("task %+v %v", task, err)
	}
	if n := repairDecisionCount(t, h, 1); n != 1 {
		t.Fatalf("retry must not insert another selected, got %d", n)
	}
}

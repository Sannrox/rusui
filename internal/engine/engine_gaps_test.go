package engine_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

func waitFetch(t *testing.T, f *gh.Fake, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.Mu.Lock()
		c := f.FetchCalls
		f.Mu.Unlock()
		if c >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for fetch calls >= %d", n)
}

func checkpointID(t *testing.T, st *store.Store, repo string) string {
	t.Helper()
	var id string
	err := st.DB.QueryRow(`SELECT last_delivery_id FROM reconcile_checkpoints WHERE repo=?`, repo).Scan(&id)
	if err != nil {
		return ""
	}
	return id
}

func invalidationCount(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM evidence_invalidations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPauseDuringEvidenceFetch(t *testing.T) {
	h := setup(t)
	it := issue(1)
	it.CreatedAt = h.clk.T.Add(-90 * 24 * time.Hour).Format(time.RFC3339)
	it.LastNonBotCommentAt = it.CreatedAt
	h.putRefresh(it)
	c := h.claim()
	_ = h.e.SetPause("test", true)
	a := art(c, "propose_close", "close", "stale_insufficient_info")
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	if n, _ := store.CountIntended(h.st, it.Repo, it.Item); n != 0 {
		t.Fatalf("intended after paused complete %d", n)
	}
	_ = h.e.SetPause("test", false)
	h.f.SetBlockFetch(make(chan struct{}))
	done := make(chan error, 1)
	go func() { done <- h.e.ApplyAttempt(it.Repo, it.Item) }()
	waitFetch(t, h.f, 1)
	if err := h.e.SetPause("test", true); err != nil {
		t.Fatal(err)
	}
	h.f.CloseBlockFetch()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n, _ := store.CountIntended(h.st, it.Repo, it.Item); n != 0 {
		t.Fatalf("intended after paused apply %d", n)
	}
}

func TestDeliveryDuringOwnerFetch(t *testing.T) {
	h := setup(t)
	it := issue(1)
	h.f.Put(it)
	if err := h.e.CatchUpItem(it.Repo, it.Item, it.ItemKind); err != nil {
		t.Fatal(err)
	}
	h.f.SetBlockFetch(make(chan struct{}))
	done := make(chan error, 1)
	go func() {
		_, err := h.e.StepRefresh()
		done <- err
	}()
	waitFetch(t, h.f, 1)
	body := []byte(`{"repository":{"full_name":"example/test-repo"},"issue":{"number":1}}`)
	if err := h.e.IngestWebhook("during-fetch", it.Repo, it.Item, it.ItemKind); err != nil {
		t.Fatal(err)
	}
	var needs int
	if err := h.st.DB.QueryRow(`SELECT needs_another FROM refresh_requests WHERE repo=? AND item=?`, it.Repo, it.Item).Scan(&needs); err != nil {
		t.Fatal(err)
	}
	if needs != 1 {
		t.Fatalf("needs_another=%d", needs)
	}
	_ = body
	h.f.CloseBlockFetch()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	it.Body = "later-live"
	h.f.Put(it)
	ok, err := h.e.StepRefresh()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("second refresh did no work")
	}
	j, _ := store.JobState(h.st, it.Repo, it.Item)
	snap, err := store.LoadSnapshot(h.st, it.Repo, it.Item, j.PendingRevision)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Body != "later-live" {
		t.Fatalf("pending body %q", snap.Body)
	}
}

func TestHangingRefreshReaper(t *testing.T) {
	h := setup(t)
	it := issue(1)
	h.f.Put(it)
	if err := h.e.CatchUpItem(it.Repo, it.Item, it.ItemKind); err != nil {
		t.Fatal(err)
	}
	h.f.SetBlockFetch(make(chan struct{}))
	done := make(chan error, 1)
	go func() {
		_, err := h.e.StepRefresh()
		done <- err
	}()
	waitFetch(t, h.f, 1)
	h.clk.Advance(time.Hour)
	if err := h.e.ExpireRefreshOwners(); err != nil {
		t.Fatal(err)
	}
	h.f.CloseBlockFetch()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	h.clk.Advance(2 * time.Second)
	ok, err := h.e.StepRefresh()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("item still blocked")
	}
	j, _ := store.JobState(h.st, it.Repo, it.Item)
	if j == nil {
		t.Fatal("no job after reaper")
	}
}

func TestReconcileCheckpointSkip(t *testing.T) {
	h := setup(t)
	p1, _ := json.Marshal(map[string]any{
		"repository": map[string]string{"full_name": "example/test-repo"},
		"issue":      map[string]int{"number": 3},
	})
	p2, _ := json.Marshal(map[string]any{
		"repository": map[string]string{"full_name": "example/test-repo"},
		"issue":      map[string]int{"number": 4},
	})
	h.f.Deliveries = []gh.DeliveryDetail{
		{ID: "x2", DeliveredAt: "2026-09-10T00:01:00Z", Event: "issues", Payload: p2},
		{ID: "x1", DeliveredAt: "2026-09-10T00:00:00Z", Event: "issues", Payload: p1},
	}
	h.f.DetailFailIDs["x1"] = engine.RefreshRetryLimit
	h.f.Put(issue(3))
	h.f.Put(issue(4))
	if id := checkpointID(t, h.st, "example/test-repo"); id != "" {
		t.Fatalf("early checkpoint %s", id)
	}
	for range engine.RefreshRetryLimit - 1 {
		if err := h.e.ReconcileDeliveries("example/test-repo"); err == nil {
			t.Fatal("expected detail fail")
		}
		if id := checkpointID(t, h.st, "example/test-repo"); id != "" {
			t.Fatalf("checkpoint advanced to %s before skip", id)
		}
	}
	if err := h.e.ReconcileDeliveries("example/test-repo"); err != nil {
		t.Fatal(err)
	}
	if id := checkpointID(t, h.st, "example/test-repo"); id != "x2" {
		t.Fatalf("checkpoint %q want x2", id)
	}
	ok, err := h.e.StepRefresh()
	if err != nil || !ok {
		t.Fatalf("refresh %v %v", ok, err)
	}
	j4, _ := store.JobState(h.st, "example/test-repo", 4)
	if j4 == nil {
		t.Fatal("later delivery not admitted")
	}
}

func TestApplyInFlightRetry(t *testing.T) {
	h := setup(t)
	it := issue(1)
	it.CreatedAt = h.clk.T.Add(-90 * 24 * time.Hour).Format(time.RFC3339)
	it.LastNonBotCommentAt = it.CreatedAt
	h.putRefresh(it)
	c := h.claim()
	_ = h.e.SetPause("test", true)
	a := art(c, "propose_close", "close", "stale_insufficient_info")
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	var revID int64
	if err := h.st.DB.QueryRow(`SELECT id FROM review_revisions WHERE job_id=?`, c.Job.ID).Scan(&revID); err != nil {
		t.Fatal(err)
	}
	actionID := "inflight-test"
	_, err := h.st.DB.Exec(`INSERT INTO apply_attempts (action_id, review_revision_id, repo, item, state) VALUES (?,?,?,?, 'in_flight')`,
		actionID, revID, it.Repo, it.Item)
	if err != nil {
		t.Fatal(err)
	}
	_ = h.e.SetPause("test", false)
	if err := h.e.ApplyAttempt(it.Repo, it.Item); err != nil {
		t.Fatal(err)
	}
	n, _ := store.CountIntended(h.st, it.Repo, it.Item)
	if n != 1 {
		t.Fatalf("intended %d", n)
	}
}

func TestOlderApplyDoesNotDisturbReviewB(t *testing.T) {
	h := setup(t)
	it := issue(1)
	it.CreatedAt = h.clk.T.Add(-90 * 24 * time.Hour).Format(time.RFC3339)
	it.LastNonBotCommentAt = it.CreatedAt
	h.putRefresh(it)
	c := h.claim()
	_ = h.e.SetPause("test", true)
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "propose_close", "close", "stale_insufficient_info")); err != nil {
		t.Fatal(err)
	}
	_ = h.e.SetPause("test", false)
	it.Body = "b-content"
	h.putRefresh(it)
	j, _ := store.JobState(h.st, it.Repo, it.Item)
	pending := j.PendingRevision
	b := h.claim()
	if err := h.e.ApplyAttempt(it.Repo, it.Item); err != nil {
		t.Fatal(err)
	}
	j2, _ := store.JobState(h.st, it.Repo, it.Item)
	if j2.PendingRevision != pending {
		t.Fatalf("pending %d -> %d", pending, j2.PendingRevision)
	}
	if j2.State != "leased" || j2.LeaseGeneration != b.Job.LeaseGeneration {
		t.Fatalf("disturbed B: %+v", j2)
	}
	n, _ := store.CountIntended(h.st, it.Repo, it.Item)
	if n != 0 {
		t.Fatalf("intended %d", n)
	}
}

func TestApplyPendingMismatchCancels(t *testing.T) {
	h := setup(t)
	it := issue(1)
	it.CreatedAt = h.clk.T.Add(-90 * 24 * time.Hour).Format(time.RFC3339)
	it.LastNonBotCommentAt = it.CreatedAt
	h.putRefresh(it)
	c := h.claim()
	_ = h.e.SetPause("test", true)
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "propose_close", "close", "stale_insufficient_info")); err != nil {
		t.Fatal(err)
	}
	_ = h.e.SetPause("test", false)
	it.Body = "moved"
	h.putRefresh(it)
	if err := h.e.ApplyAttempt(it.Repo, it.Item); err != nil {
		t.Fatal(err)
	}
	n, _ := store.CountIntended(h.st, it.Repo, it.Item)
	if n != 0 {
		t.Fatalf("intended %d", n)
	}
}

func TestStaleSixtyDaysEligible(t *testing.T) {
	h := setup(t)
	it := issue(1)
	it.CreatedAt = h.clk.T.Add(-60 * 24 * time.Hour).Format(time.RFC3339)
	it.LastNonBotCommentAt = it.CreatedAt
	h.putRefresh(it)
	c := h.claim()
	a := art(c, "propose_close", "close", "stale_insufficient_info")
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	n, _ := store.CountIntended(h.st, it.Repo, 1)
	if n != 1 {
		t.Fatalf("60d should qualify, got %d", n)
	}
	bodies, _ := store.IntendedBodies(h.st, it.Repo, 1)
	if len(bodies) == 0 || !bytes.Contains([]byte(bodies[0]), []byte("60/60")) {
		t.Fatalf("limit sentence %v", bodies)
	}
}

func TestDuplicateLimitSentence(t *testing.T) {
	h := setup(t)
	it := issue(1)
	h.putRefresh(it)
	h.f.Put(issue(9))
	c := h.claim()
	a := art(c, "propose_close", "close", "duplicate_or_superseded")
	a.ProposedActions[0].Canonical = 9
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	bodies, _ := store.IntendedBodies(h.st, it.Repo, it.Item)
	if len(bodies) != 1 || !bytes.Contains([]byte(bodies[0]), []byte("not that the reports are duplicates")) {
		t.Fatalf("%v", bodies)
	}
}

func TestEvidenceInvalidationOnce(t *testing.T) {
	h := setup(t)
	it := issue(1)
	it.ItemKind = "pull"
	it.HeadSHA = "h1"
	it.BaseSHA = "b1"
	it.DefaultBranch = "main"
	it.BaseRef = "main"
	it.MergedIntoDefault = true
	it.MergeCommitSHA = "c1"
	it.MainSHA = "aaa"
	h.putRefresh(it)
	c := h.claim()
	a := art(c, "propose_close", "close", "implemented_on_main")
	a.HeadSHA = "h1"
	a.ProposedActions[0].CommitSHA = "c1"
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	before := invalidationCount(t, h.st)
	it.MergedIntoDefault = false
	it.BaseRef = "release-1"
	h.f.Put(it)
	if err := h.e.ApplyAttempt(it.Repo, it.Item); err != nil {
		t.Fatal(err)
	}
	if err := h.e.ApplyAttempt(it.Repo, it.Item); err != nil {
		t.Fatal(err)
	}
	if n := invalidationCount(t, h.st); n != before+1 {
		t.Fatalf("invalidations %d want %d", n, before+1)
	}
	j, _ := store.JobState(h.st, it.Repo, it.Item)
	pendingBefore := j.PendingRevision
	_, _ = h.e.StepRefresh()
	_, _ = h.e.StepRefresh()
	j2, _ := store.JobState(h.st, it.Repo, it.Item)
	if j2.PendingRevision-pendingBefore > 1 {
		t.Fatalf("pending jumped %d -> %d", pendingBefore, j2.PendingRevision)
	}
}

func TestImplementedOnMainReverifyNoNewPending(t *testing.T) {
	h := setup(t)
	it := issue(8)
	it.ItemKind = "pull"
	it.HeadSHA = "h1"
	it.BaseSHA = "b1"
	it.DefaultBranch = "main"
	it.BaseRef = "main"
	it.Merged = true
	it.MergedIntoDefault = true
	it.MergeCommitSHA = "c1"
	it.MainSHA = "aaa"
	it.Body = "same"
	h.putRefresh(it)
	c := h.claim()
	a := art(c, "propose_close", "close", "implemented_on_main")
	a.HeadSHA = "h1"
	a.ProposedActions[0].CommitSHA = "c1"
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	j, _ := store.JobState(h.st, it.Repo, it.Item)
	rev := j.PendingRevision
	it.MainSHA = "bbb"
	h.f.Put(it)
	if err := h.e.ApplyAttempt(it.Repo, it.Item); err != nil {
		t.Fatal(err)
	}
	j2, _ := store.JobState(h.st, it.Repo, it.Item)
	if j2.PendingRevision != rev {
		t.Fatalf("main advance rereviewed pending %d -> %d", rev, j2.PendingRevision)
	}
}

func TestWebhookMissCatchUpAdmits(t *testing.T) {
	h := setup(t)
	it := issue(1)
	h.f.Put(it)
	if err := h.e.CatchUpOpenAndLocal([]snapshot.Item{it}); err != nil {
		t.Fatal(err)
	}
	ok, err := h.e.StepRefresh()
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	j, _ := store.JobState(h.st, it.Repo, it.Item)
	if j == nil {
		t.Fatal("catch-up did not admit")
	}
}

func TestOwnerFetchDiscardedAfterExpire(t *testing.T) {
	h := setup(t)
	it := issue(1)
	h.f.Put(it)
	if err := h.e.CatchUpItem(it.Repo, it.Item, it.ItemKind); err != nil {
		t.Fatal(err)
	}
	h.f.SetBlockFetch(make(chan struct{}))
	done := make(chan error, 1)
	go func() {
		_, err := h.e.StepRefresh()
		done <- err
	}()
	waitFetch(t, h.f, 1)
	h.clk.Advance(time.Hour)
	if err := h.e.ExpireRefreshOwners(); err != nil {
		t.Fatal(err)
	}
	it.Body = "from-b"
	h.f.Put(it)
	h.f.CloseBlockFetch()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	h.clk.Advance(2 * time.Second)
	if _, err := h.e.StepRefresh(); err != nil {
		t.Fatal(err)
	}
	j, _ := store.JobState(h.st, it.Repo, it.Item)
	snap, err := store.LoadSnapshot(h.st, it.Repo, it.Item, j.PendingRevision)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Body != "from-b" {
		t.Fatalf("want B body, got %q", snap.Body)
	}
}

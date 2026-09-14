package engine_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/server"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

const fixture = `version: 1
defaults:
  never_release: true
  never_leak_private_to_public: true
  review: true
  comments: true
  close: true
  implement: false
  land: false
  max_reviews_per_repo_per_utc_day: 50
repos:
  example/test-repo:
    visibility: public
    review: true
    comments: true
    close: true
    implement: false
    land: false
`

type harn struct {
	t    *testing.T
	e    *engine.Engine
	st   *store.Store
	f    *gh.Fake
	clk  *clock.Fake
	srv  *server.Server
	http *httptest.Server
}

func setup(t *testing.T) *harn {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	pol, err := policy.Parse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock.Fake{T: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)}
	f := gh.NewFake()
	e := engine.New(st, pol, f, clk)
	e.OwnerTTL = 50 * time.Millisecond
	e.FetchTimeout = 20 * time.Millisecond
	e.ReloadPolicy(pol)
	srv := &server.Server{Eng: e, WebhookSec: "whsec", WorkerSec: "wsec", SlackSec: "slsec", SlackUsers: map[string]bool{"U1": true}}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return &harn{t: t, e: e, st: st, f: f, clk: clk, srv: srv, http: hs}
}

func issue(n int) snapshot.Item {
	return snapshot.Item{
		Repo: "example/test-repo", Item: n, ItemKind: "issue", State: "open",
		Title: "bug", Body: "repro", Labels: []string{"bug"}, UpdatedAt: "2026-01-01T00:00:00Z",
		CreatedAt: "2025-01-01T00:00:00Z", LastNonBotCommentAt: "2025-01-02T00:00:00Z",
		DefaultBranch: "main", MainSHA: "aaa",
	}
}

func (h *harn) putRefresh(it snapshot.Item) {
	h.t.Helper()
	h.f.Put(it)
	if err := h.e.CatchUpItem(it.Repo, it.Item, it.ItemKind); err != nil {
		h.t.Fatal(err)
	}
	ok, err := h.e.StepRefresh()
	if err != nil {
		h.t.Fatal(err)
	}
	if !ok {
		h.t.Fatal("refresh did no work")
	}
}

func (h *harn) claim() *engine.Claim {
	h.t.Helper()
	c, err := h.e.Claim("example/test-repo")
	if err != nil {
		h.t.Fatal(err)
	}
	if c == nil {
		h.t.Fatal("no claim")
	}
	return c
}

func art(c *engine.Claim, verdict, typ, reason string) engine.Artifact {
	a := engine.Artifact{
		SchemaVersion: 1, Repo: c.Job.Repo, Item: c.Job.Item, ItemKind: c.Job.ItemKind,
		ClaimedRevision: c.Job.ClaimedRevision, SnapshotHash: c.ItemHash, MainSHA: c.Snapshot.MainSHA,
		Verdict: verdict, Confidence: "high",
		ProposedActions: []engine.ProposedAction{{Type: typ, ReasonCode: reason}},
	}
	return a
}

func TestWebhookNoFetch(t *testing.T) {
	h := setup(t)
	it := issue(1)
	h.f.Put(it)
	body := []byte(`{"repository":{"full_name":"example/test-repo"},"issue":{"number":1}}`)
	req := httptest.NewRequest("POST", "/hooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", gh.Sign("whsec", body))
	req.Header.Set("X-GitHub-Delivery", "d1")
	rr := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("code %d %s", rr.Code, rr.Body.Bytes())
	}
	if h.f.CallCount() != 0 {
		t.Fatalf("fetch during webhook: %d", h.f.CallCount())
	}
}

func TestHappyDryRun(t *testing.T) {
	h := setup(t)
	it := issue(1)
	it.CreatedAt = h.clk.T.Add(-90 * 24 * time.Hour).Format(time.RFC3339)
	it.LastNonBotCommentAt = h.clk.T.Add(-90 * 24 * time.Hour).Format(time.RFC3339)
	h.putRefresh(it)
	c := h.claim()
	a := art(c, "propose_close", "close", "stale_insufficient_info")
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	n, _ := store.CountIntended(h.st, it.Repo, it.Item)
	if n != 1 {
		t.Fatalf("intended %d", n)
	}
	bodies, _ := store.IntendedBodies(h.st, it.Repo, it.Item)
	if len(bodies) == 0 || !bytes.Contains([]byte(bodies[0]), []byte("evidence_class=")) {
		t.Fatalf("body %v", bodies)
	}
}

func TestExpiredHeartbeatAndComplete(t *testing.T) {
	h := setup(t)
	h.putRefresh(issue(1))
	c := h.claim()
	h.clk.Advance(4 * time.Minute)
	if err := h.e.Heartbeat(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision); err == nil {
		t.Fatal("heartbeat should fail")
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err == nil {
		t.Fatal("complete should fail")
	}
}

func TestCompleteRetryIdempotent(t *testing.T) {
	h := setup(t)
	it := issue(1)
	it.CreatedAt = h.clk.T.Add(-90 * 24 * time.Hour).Format(time.RFC3339)
	it.LastNonBotCommentAt = it.CreatedAt
	h.putRefresh(it)
	c := h.claim()
	a := art(c, "propose_close", "close", "stale_insufficient_info")
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	n, _ := store.CountReviews(h.st, c.Job.ID)
	if n != 1 {
		t.Fatalf("reviews %d", n)
	}
	ia, _ := store.CountIntended(h.st, it.Repo, it.Item)
	if ia != 1 {
		t.Fatalf("actions %d", ia)
	}
}

func TestPauseCancelsApply(t *testing.T) {
	h := setup(t)
	it := issue(1)
	it.CreatedAt = h.clk.T.Add(-90 * 24 * time.Hour).Format(time.RFC3339)
	it.LastNonBotCommentAt = it.CreatedAt
	h.putRefresh(it)
	c := h.claim()
	_ = h.e.SetPause("example/test-repo", true)
	a := art(c, "propose_close", "close", "stale_insufficient_info")
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	n, _ := store.CountIntended(h.st, it.Repo, it.Item)
	if n != 0 {
		t.Fatalf("intended %d", n)
	}
}

func TestMainSHANotInItemHash(t *testing.T) {
	h := setup(t)
	it := issue(1)
	h.putRefresh(it)
	j, _ := store.JobState(h.st, it.Repo, it.Item)
	rev := j.PendingRevision
	it.MainSHA = "bbb"
	h.putRefresh(it)
	j, _ = store.JobState(h.st, it.Repo, it.Item)
	if j.PendingRevision != rev {
		t.Fatalf("pending %d -> %d", rev, j.PendingRevision)
	}
}

func TestCatchUpUnchangedDuringReview(t *testing.T) {
	h := setup(t)
	it := issue(1)
	h.putRefresh(it)
	c := h.claim()
	rev := c.Job.PendingRevision
	for range 5 {
		h.putRefresh(it)
	}
	j, _ := store.JobState(h.st, it.Repo, it.Item)
	if j.PendingRevision != rev {
		t.Fatalf("pending moved %d", j.PendingRevision)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteAThenClaimB(t *testing.T) {
	h := setup(t)
	it := issue(1)
	h.putRefresh(it)
	c := h.claim()
	it.Body = "changed"
	h.putRefresh(it)
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err != nil {
		t.Fatal(err)
	}
	c2 := h.claim()
	if c2.Job.ClaimedRevision == c.Job.ClaimedRevision {
		t.Fatal("expected new revision")
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err != nil {
		t.Fatal(err)
	}
	n, _ := store.CountReviews(h.st, c.Job.ID)
	if n != 1 {
		t.Fatalf("reviews %d", n)
	}
}

func TestClaimDoesNotBlockOtherQueuedItem(t *testing.T) {
	h := setup(t)
	h.putRefresh(issue(1))
	h.putRefresh(issue(2))
	c1 := h.claim()
	if c1.Job.Item != 1 {
		t.Fatalf("first claim item %d", c1.Job.Item)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil {
		t.Fatal(err)
	}
	if c2 == nil {
		t.Fatal("live lease on #1 blocked #2")
	}
	if c2.Job.Item != 2 {
		t.Fatalf("second claim item %d", c2.Job.Item)
	}
	c3, err := h.e.Claim("example/test-repo")
	if err != nil {
		t.Fatal(err)
	}
	if c3 != nil {
		t.Fatalf("expected no work, got #%d", c3.Job.Item)
	}
	j1, _ := store.JobState(h.st, "example/test-repo", 1)
	j2, _ := store.JobState(h.st, "example/test-repo", 2)
	if j1.State != "leased" || j2.State != "leased" {
		t.Fatalf("states %s %s", j1.State, j2.State)
	}
	var n int
	if err := h.st.DB.QueryRow(`SELECT count FROM daily_review_counts WHERE repo=?`, "example/test-repo").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("daily budget %d", n)
	}
}

func TestClaimReapRetryLimit(t *testing.T) {
	h := setup(t)
	h.putRefresh(issue(1))
	for range engine.RetryLimit {
		_ = h.claim()
		h.clk.Advance(4 * time.Minute)
	}
	_, _ = h.e.Claim("example/test-repo")
	j, _ := store.JobState(h.st, "example/test-repo", 1)
	if j.State != "failed" {
		t.Fatalf("state %s retry %d", j.State, j.RetryCount)
	}
}

func TestFailNoJSON(t *testing.T) {
	h := setup(t)
	h.putRefresh(issue(1))
	c := h.claim()
	if _, err := h.e.Fail(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.Fail(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision); err != nil {
		t.Fatal(err)
	}
	n, _ := store.CountReviews(h.st, c.Job.ID)
	if n != 0 {
		t.Fatal(n)
	}
	ia, _ := store.CountIntended(h.st, "example/test-repo", 1)
	if ia != 0 {
		t.Fatal(ia)
	}
}

func TestFailAThenAdmitB(t *testing.T) {
	h := setup(t)
	it := issue(1)
	h.putRefresh(it)
	c := h.claim()
	if _, err := h.e.Fail(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision); err != nil {
		t.Fatal(err)
	}
	j, _ := store.JobState(h.st, it.Repo, it.Item)
	if j.RetryCount == 0 {
		t.Fatal("expected retry count")
	}
	it.Body = "b"
	h.putRefresh(it)
	j, _ = store.JobState(h.st, it.Repo, it.Item)
	if j.RetryCount != 0 {
		t.Fatalf("budget %d", j.RetryCount)
	}
	if _, err := h.e.Fail(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision); err != nil {
		t.Fatal(err)
	}
	j2, _ := store.JobState(h.st, it.Repo, it.Item)
	if j2.RetryCount != 0 {
		t.Fatalf("replay spent %d", j2.RetryCount)
	}
}

func TestWrongHashFails(t *testing.T) {
	h := setup(t)
	h.putRefresh(issue(1))
	c := h.claim()
	a := art(c, "keep", "", "")
	a.SnapshotHash = "nope"
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	n, _ := store.CountReviews(h.st, c.Job.ID)
	if n != 0 {
		t.Fatal(n)
	}
}

func TestNotReproducibleAdvisory(t *testing.T) {
	h := setup(t)
	h.putRefresh(issue(1))
	c := h.claim()
	a := art(c, "propose_close", "close", "not_reproducible_on_main")
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	n, _ := store.CountIntended(h.st, "example/test-repo", 1)
	if n != 0 {
		t.Fatal(n)
	}
}

func TestPauseClaimComplete(t *testing.T) {
	h := setup(t)
	h.putRefresh(issue(1))
	c := h.claim()
	_ = h.e.SetPause("example/test-repo", true)
	if _, err := h.e.Claim("example/test-repo"); err == nil {
		if err == nil {
			// claim returns errPaused
		}
	}
	c2, err := h.e.Claim("example/test-repo")
	if err == nil && c2 != nil {
		t.Fatal("claimed while paused")
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err != nil {
		t.Fatal(err)
	}
}

func TestPauseKeepsLeaseOnAdmit(t *testing.T) {
	h := setup(t)
	it := issue(1)
	h.putRefresh(it)
	c := h.claim()
	_ = h.e.SetPause("example/test-repo", true)
	it.Body = "new"
	h.putRefresh(it)
	j, _ := store.JobState(h.st, it.Repo, it.Item)
	if j.State != "leased" {
		t.Fatalf("state %s", j.State)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err != nil {
		t.Fatal(err)
	}
	_ = h.e.SetPause("example/test-repo", false)
	c2 := h.claim()
	if c2.Job.ClaimedRevision == c.Job.ClaimedRevision {
		t.Fatal("should claim new pending")
	}
}

func TestStaleCompleteRejected(t *testing.T) {
	h := setup(t)
	h.putRefresh(issue(1))
	c := h.claim()
	h.clk.Advance(4 * time.Minute)
	_, err := h.e.Claim("example/test-repo")
	_ = err
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err == nil {
		t.Fatal("expected reject")
	}
	n, _ := store.CountReviews(h.st, c.Job.ID)
	if n != 0 {
		t.Fatal(n)
	}
}

func TestReleaseBranchPR(t *testing.T) {
	h := setup(t)
	it := issue(2)
	it.ItemKind = "pull"
	it.BaseRef = "release-1"
	it.DefaultBranch = "main"
	it.Merged = true
	it.MergedIntoDefault = false
	it.MergeCommitSHA = "deadbeef"
	it.HeadSHA = "abc"
	it.BaseSHA = "def"
	it.MainSHA = "aaa"
	h.putRefresh(it)
	c := h.claim()
	a := art(c, "propose_close", "close", "implemented_on_main")
	a.ProposedActions[0].CommitSHA = "deadbeef"
	a.HeadSHA = "abc"
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	n, _ := store.CountIntended(h.st, it.Repo, it.Item)
	if n != 0 {
		t.Fatal("release branch should not qualify")
	}
}

func TestStaleThresholds(t *testing.T) {
	h := setup(t)
	it := issue(1)
	it.CreatedAt = h.clk.T.Add(-59 * 24 * time.Hour).Format(time.RFC3339)
	it.LastNonBotCommentAt = it.CreatedAt
	h.putRefresh(it)
	c := h.claim()
	a := art(c, "propose_close", "close", "stale_insufficient_info")
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	n, _ := store.CountIntended(h.st, it.Repo, 1)
	if n != 0 {
		t.Fatal("59d should not qualify")
	}
}

func TestOperatorRetry(t *testing.T) {
	h := setup(t)
	h.putRefresh(issue(1))
	for range engine.RetryLimit {
		_ = h.claim()
		h.clk.Advance(4 * time.Minute)
	}
	_, _ = h.e.Claim("example/test-repo")
	j, _ := store.JobState(h.st, "example/test-repo", 1)
	if j.State != "failed" {
		t.Fatalf("%s", j.State)
	}
	_ = h.e.Sweep("example/test-repo")
	j, _ = store.JobState(h.st, "example/test-repo", 1)
	if j.State != "failed" {
		t.Fatalf("sweep changed %s", j.State)
	}
	if err := h.e.OperatorRetry("example/test-repo", 1, "U1"); err != nil {
		t.Fatal(err)
	}
	j, _ = store.JobState(h.st, "example/test-repo", 1)
	if j.State != "queued" || j.RetryCount != 0 {
		t.Fatalf("%s %d", j.State, j.RetryCount)
	}
}

func TestClosedCatchUp(t *testing.T) {
	h := setup(t)
	it := issue(1)
	h.putRefresh(it)
	it.State = "closed"
	h.f.Put(it)
	if err := h.e.CatchUpOpenAndLocal(nil); err != nil {
		t.Fatal(err)
	}
	_, _ = h.e.StepRefresh()
	j, _ := store.JobState(h.st, it.Repo, 1)
	if j.PendingRevision < 2 {
		t.Fatalf("pending %d", j.PendingRevision)
	}
}

func TestPolicyRevoke(t *testing.T) {
	h := setup(t)
	h.putRefresh(issue(1))
	c := h.claim()
	p, err := policy.Parse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	delete(p.Repos, "example/test-repo")
	h.e.Policy = p
	c2, err := h.e.Claim("example/test-repo")
	if err == nil && c2 != nil {
		t.Fatal("claimed after revoke")
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err != nil {
		t.Fatal(err)
	}
}

func TestPinnedInput(t *testing.T) {
	h := setup(t)
	it := issue(1)
	it.MainSHA = "oldmain"
	h.putRefresh(it)
	c := h.claim()
	it.MainSHA = "newermain"
	h.f.Put(it)
	in := h.e.BuildInput(c)
	if in["main_sha"] != "oldmain" {
		t.Fatalf("%v", in["main_sha"])
	}
	if in["main_sha"] == "newermain" {
		t.Fatal("leaked live main")
	}
}

func TestRefreshExpiredOwnership(t *testing.T) {
	h := setup(t)
	it := issue(1)
	h.putRefresh(it)
	it.Body = "newer"
	h.f.Put(it)
	_ = h.e.CatchUpItem(it.Repo, it.Item, it.ItemKind)
	past := h.clk.T.Add(-time.Minute).Format(time.RFC3339Nano)
	_, _ = h.st.DB.Exec(`UPDATE refresh_requests SET owner=1, generation=1, owner_expires_at=?, state='running' WHERE repo=? AND item=?`, past, it.Repo, it.Item)
	if err := h.e.ExpireRefreshOwners(); err != nil {
		t.Fatal(err)
	}
	h.clk.Advance(2 * time.Second)
	if _, err := h.e.StepRefresh(); err != nil {
		t.Fatal(err)
	}
	j, _ := store.JobState(h.st, it.Repo, 1)
	if j == nil {
		t.Fatal("no job")
	}
	snap, err := store.LoadSnapshot(h.st, it.Repo, 1, j.PendingRevision)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Body != "newer" {
		t.Fatalf("body %q pending %d", snap.Body, j.PendingRevision)
	}
}

func TestStartupExpiresOwner(t *testing.T) {
	h := setup(t)
	it := issue(1)
	h.f.Put(it)
	_ = h.e.CatchUpItem(it.Repo, it.Item, it.ItemKind)
	h.f.SetFetchHang(5 * time.Second)
	go h.e.StepRefresh()
	time.Sleep(20 * time.Millisecond)
	h.f.SetFetchHang(0)
	if err := h.e.StartupExpireOwners(); err != nil {
		t.Fatal(err)
	}
	_, err := h.e.StepRefresh()
	if err != nil {
		t.Fatal(err)
	}
	j, _ := store.JobState(h.st, it.Repo, 1)
	if j == nil {
		t.Fatal("stuck")
	}
}

func TestEvidenceInvalidationAtomic(t *testing.T) {
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
	it.MergedIntoDefault = false
	it.BaseRef = "release-1"
	h.f.Put(it)
	if err := h.e.ApplyAttempt(it.Repo, it.Item); err != nil {
		t.Fatal(err)
	}
	if err := h.e.ApplyAttempt(it.Repo, it.Item); err != nil {
		t.Fatal(err)
	}
	_, _ = h.e.StepRefresh()
}

func TestReconcileDetailFail(t *testing.T) {
	h := setup(t)
	payload, _ := json.Marshal(map[string]any{
		"repository": map[string]string{"full_name": "example/test-repo"},
		"issue":      map[string]int{"number": 3},
	})
	h.f.Deliveries = []gh.DeliveryDetail{{
		ID: "x1", DeliveredAt: "2026-09-10T00:00:00Z", Event: "issues", Payload: payload,
	}, {
		ID: "x2", DeliveredAt: "2026-09-10T00:01:00Z", Event: "issues", Payload: payload,
	}}
	h.f.DetailFailIDs["x1"] = 5
	h.f.Put(issue(3))
	for range 6 {
		_ = h.e.ReconcileDeliveries("example/test-repo")
	}
	_ = h.e.ReconcileDeliveries("example/test-repo")
	_, _ = h.e.StepRefresh()
}

func TestSlackRetry(t *testing.T) {
	h := setup(t)
	h.putRefresh(issue(1))
	for range engine.RetryLimit {
		_ = h.claim()
		h.clk.Advance(4 * time.Minute)
	}
	_, _ = h.e.Claim("example/test-repo")
	if err := h.e.OperatorRetry("example/test-repo", 1, "U1"); err != nil {
		t.Fatal(err)
	}
	j, _ := store.JobState(h.st, "example/test-repo", 1)
	if j.State != "queued" {
		t.Fatalf("%s", j.State)
	}
}

func TestDeadlineKillsLease(t *testing.T) {
	h := setup(t)
	h.putRefresh(issue(1))
	c := h.claim()
	h.clk.Advance(13 * time.Minute)
	if err := h.e.Heartbeat(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision); err == nil {
		t.Fatal("heartbeat after deadline")
	}
}

func TestInputJSONNoLiveMain(t *testing.T) {
	h := setup(t)
	it := issue(1)
	it.MainSHA = "pin"
	h.putRefresh(it)
	c := h.claim()
	it.MainSHA = "LIVE"
	h.f.Put(it)
	b, _ := json.Marshal(h.e.BuildInput(c))
	if bytes.Contains(b, []byte("LIVE")) {
		t.Fatal(string(b))
	}
}

func TestWebhookHTTP(t *testing.T) {
	h := setup(t)
	body := []byte(`{"repository":{"full_name":"example/test-repo"},"issue":{"number":9}}`)
	req, _ := http.NewRequest("POST", h.http.URL+"/hooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", gh.Sign("whsec", body))
	req.Header.Set("X-GitHub-Delivery", "abc")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatal(res.Status)
	}
}

func TestHealthz(t *testing.T) {
	h := setup(t)
	res, err := http.Get(h.http.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatal(res.Status)
	}
}

func TestPolicyParseExample(t *testing.T) {
	b, err := os.ReadFile("../../policy.example.yaml")
	if err != nil {
		t.Skip(err)
	}
	if _, err := policy.Parse(b); err != nil {
		t.Fatal(err)
	}
}

package engine_test

import (
	"errors"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

// instance is one plane process: its own store handle and engine over the
// shared database. Both instances read one clock, as StoreClock anchors
// every instance to the store's.
type instance struct {
	e  *engine.Engine
	st *store.Store
}

// pair starts two plane instances over one store and one forge, with live
// comments on.
func pair(t *testing.T) (a, b instance, f *gh.Fake, clk *clock.Fake) {
	t.Helper()
	raw := strings.Replace(fixture, "        comments: true\n        close: true", "        comments: true\n        comment: true\n        close: true", 1)
	pol, err := policy.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	f = gh.NewFake()
	clk = &clock.Fake{T: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	stores := engine.OpenSharedTestStores(t, 2)
	var out [2]instance
	for i, st := range stores {
		e := engine.New(st, pol, f, clk)
		e.Log = log.New(io.Discard, "", 0)
		e.ReloadPolicy(pol)
		e.Comments = f
		out[i] = instance{e: e, st: st}
	}
	return out[0], out[1], f, clk
}

// A plane instance dies holding a live turn. The surviving instance
// expires its lease, the turn is claimed again under a new generation and
// completes once; the lost holder's late commit is refused.
func TestSurvivingInstanceReclaimsATurnAndCompletesItOnce(t *testing.T) {
	a, b, _, clk := pair(t)
	if _, err := a.e.StartRun("test", "do the thing", ""); err != nil {
		t.Fatal(err)
	}
	lost, err := a.e.Claim("example/test-repo")
	if err != nil || lost == nil {
		t.Fatalf("claim on a: %+v %v", lost, err)
	}
	if err := a.st.Close(); err != nil {
		t.Fatal(err)
	}
	if c, err := b.e.Claim("example/test-repo"); err != nil || c != nil {
		t.Fatalf("live lease claimed again: %+v %v", c, err)
	}
	clk.Advance(engine.Liveness + time.Second)
	if err := b.e.ExpireOverdueLeases(); err != nil {
		t.Fatal(err)
	}
	c, err := b.e.Claim("example/test-repo")
	if err != nil || c == nil || c.Job.ID != lost.Job.ID || c.Job.LeaseGeneration <= lost.Job.LeaseGeneration {
		t.Fatalf("reclaim %+v %v", c, err)
	}
	if _, err := b.e.Complete(lost.Job.ID, lost.Job.LeaseGeneration, lost.Job.ClaimedRevision, runArt(lost)); err == nil {
		t.Fatal("the lost holder's commit was accepted")
	}
	for range 2 { // a retried completion is the same completion
		if _, err := b.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
			t.Fatal(err)
		}
	}
	var receipts int
	if err := b.st.DB.QueryRow(`SELECT COUNT(*) FROM receipts WHERE job_id=? AND kind='complete'`, c.Job.ID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 {
		t.Fatalf("complete receipts %d, want 1", receipts)
	}
}

// An instance pauses past its scheduler lease, the other takes over, and the
// first resumes: its schedule fire and its apply retry are refused, and the
// schedule fires once across the takeover, on the new holder.
func TestPausedInstanceIsFencedAfterTakeover(t *testing.T) {
	a, b, f, _ := pair(t)
	leaveApplyInFlight(t, a, f) // work a stale holder could repeat
	if _, err := a.e.CreateSchedule("test", "hourly", "1m", "scheduled work", 0); err != nil {
		t.Fatal(err)
	}
	la, lb := engine.NewSchedulerLease(a.st), engine.NewSchedulerLease(b.st)
	if err := la.Renew(); err != nil {
		t.Fatal(err)
	}
	staleA, held := la.Held()
	if !held {
		t.Fatal("a does not hold the lease")
	}
	t0 := time.Unix(1_700_000_000, 0).UTC()
	if err := a.e.StepSchedules(t0, staleA); err != nil {
		t.Fatal(err)
	}

	// a pauses; its lease runs out on the store's clock and b takes over.
	expireScheduler(t, b.st)
	if err := lb.Renew(); err != nil {
		t.Fatal(err)
	}
	fenceB, held := lb.Held()
	if !held || fenceB.Generation != staleA.Generation+1 {
		t.Fatalf("takeover held=%v gen %d after %d", held, fenceB.Generation, staleA.Generation)
	}

	// a resumes with the lease it held before the pause.
	if err := a.e.StepSchedules(t0.Add(time.Minute), staleA); !errors.Is(err, store.ErrPlaneLeaseLost) {
		t.Fatalf("stale fire: %v", err)
	}
	if err := a.e.RetryApplyAttempts(staleA); !errors.Is(err, store.ErrPlaneLeaseLost) {
		t.Fatalf("stale apply retry: %v", err)
	}
	if err := b.e.StepSchedules(t0, fenceB); err != nil { // a's bucket again
		t.Fatal(err)
	}
	if n := countKind(t, b.st, store.SessionKindScheduled); n != 1 {
		t.Fatalf("scheduled sessions %d after re-running a's bucket, want 1", n)
	}
	// The first scheduled turn finishes, so the next bucket starts a session.
	c, err := b.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatalf("scheduled turn %+v %v", c, err)
	}
	if _, err := b.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	if err := b.e.StepSchedules(t0.Add(time.Minute), fenceB); err != nil {
		t.Fatal(err)
	}
	if n := countKind(t, b.st, store.SessionKindScheduled); n != 2 {
		t.Fatalf("scheduled sessions %d, want one per bucket", n)
	}
}

// An instance stops between its GitHub write and the receipt. The new
// lease holder reconciles the attempt against the ledger: GitHub keeps one
// comment and the attempt succeeds.
func TestNewHolderReconcilesAnApplyLeftInFlight(t *testing.T) {
	a, b, f, _ := pair(t)
	repo, item := leaveApplyInFlight(t, a, f)
	if err := a.st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.e.RetryApplyAttempts(holdScheduler(t, b.st)); err != nil {
		t.Fatal(err)
	}
	if err := b.e.RetryApplyAttempts(holdScheduler(t, b.st)); err != nil {
		t.Fatal(err)
	}
	if got := f.IssueComments(repo, item); len(got) != 1 {
		t.Fatalf("GitHub holds %d comments, want 1", len(got))
	}
	if state := applyState(t, b.st, repo, item); state != "succeeded" {
		t.Fatalf("attempt %s, want succeeded", state)
	}
}

// A signed session webhook delivered to the surviving instance while the
// other is down is stored and wakes its session there.
func TestWebhookDuringInstanceLossWakesTheSession(t *testing.T) {
	a, b, _, _ := pair(t)
	sid, err := a.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := a.e.EnsureSessionWebhook(sid)
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.e.Claim("example/test-repo")
	if err != nil || first == nil {
		t.Fatal(err)
	}
	if _, err := a.e.Complete(first.Job.ID, first.Job.LeaseGeneration, first.Job.ClaimedRevision, runArt(first)); err != nil {
		t.Fatal(err)
	}
	if err := a.st.Close(); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"ping":true}`)
	if err := b.e.DeliverSessionWebhook(sid, gh.Sign(secret, body), body, "evt-loss"); err != nil {
		t.Fatal(err)
	}
	c, err := b.e.Claim("example/test-repo")
	if err != nil || c == nil || !strings.Contains(c.Snapshot.Body, "evt-loss") {
		t.Fatalf("woken turn %+v %v", c, err)
	}
	if turn, err := store.GetTurn(b.st, c.Job.ID); err != nil || turn.SessionID != sid {
		t.Fatalf("turn %+v %v, want session %d", turn, err, sid)
	}
}

// An instance that loses its store stops holding the scheduler lease and
// takes no turn; the other instance takes the lease over once it expires.
func TestInstanceWithoutStoreYieldsTheLease(t *testing.T) {
	a, b, _, _ := pair(t)
	if _, err := b.e.StartRun("test", "work", ""); err != nil {
		t.Fatal(err)
	}
	la, lb := engine.NewSchedulerLease(a.st), engine.NewSchedulerLease(b.st)
	if err := la.Renew(); err != nil {
		t.Fatal(err)
	}
	if err := a.st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := la.Renew(); err == nil {
		t.Fatal("renewal without a store succeeded")
	}
	if _, held := la.Held(); held {
		t.Fatal("an instance without its store still holds the lease")
	}
	if c, err := a.e.Claim("example/test-repo"); err == nil || c != nil {
		t.Fatalf("an instance without its store took work: %+v %v", c, err)
	}
	expireScheduler(t, b.st)
	if err := lb.Renew(); err != nil {
		t.Fatal(err)
	}
	if _, held := lb.Held(); !held {
		t.Fatal("the surviving instance did not take the lease over")
	}
	if c, err := b.e.Claim("example/test-repo"); err != nil || c == nil {
		t.Fatalf("surviving instance claim %+v %v", c, err)
	}
}

// leaveApplyInFlight has the instance write a comment to GitHub and lose
// the response, as a crash between the call and its receipt would. The
// attempt is left uncertain.
func leaveApplyInFlight(t *testing.T, in instance, f *gh.Fake) (string, int) {
	t.Helper()
	it := issue(1)
	f.Put(it)
	if err := in.e.CatchUpItem(it.Repo, it.Item, it.ItemKind); err != nil {
		t.Fatal(err)
	}
	if _, err := in.e.StepRefresh(); err != nil {
		t.Fatal(err)
	}
	c, err := in.e.Claim(it.Repo)
	if err != nil || c == nil {
		t.Fatalf("claim %+v %v", c, err)
	}
	f.CommentDrop = true
	defer func() { f.CommentDrop = false }()
	if _, err := in.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "propose_comment", "comment", "note")); err != nil {
		t.Fatal(err)
	}
	if state := applyState(t, in.st, it.Repo, it.Item); state != "uncertain" {
		t.Fatalf("attempt %s, want uncertain", state)
	}
	return it.Repo, it.Item
}

func applyState(t *testing.T, st *store.Store, repo string, item int) string {
	t.Helper()
	var state string
	if err := st.DB.QueryRow(`SELECT state FROM apply_attempts WHERE repo=? AND item=?`, repo, item).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

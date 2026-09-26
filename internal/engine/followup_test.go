package engine_test

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

func TestPromptFollowUpQueued(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	_, pending, err := h.e.PromptFollowUp(sid, "second")
	if err != nil || pending != 2 {
		t.Fatalf("pending %d %v", pending, err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	if c.Snapshot.Body != "second" {
		t.Fatalf("body %q", c.Snapshot.Body)
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil || sess.Prompt != "second" {
		t.Fatalf("%v %v", sess, err)
	}
}

func TestPromptFollowUpFIFO(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	_, p1, err := h.e.PromptFollowUp(sid, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	_, p2, err := h.e.PromptFollowUp(sid, "beta")
	if err != nil || p2 != p1 {
		t.Fatalf("second follow-up must not overwrite pending: p1=%d p2=%d %v", p1, p2, err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	if c.Snapshot.Body != "alpha" {
		t.Fatalf("first claim body %q", c.Snapshot.Body)
	}
	a := runArt(c)
	a.GuestSessionID = "sess-fake"
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil || sess.GuestSessionID != "sess-fake" {
		t.Fatalf("guest id %+v %v", sess, err)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil {
		t.Fatal(err)
	}
	if c2.Snapshot.Body != "beta" {
		t.Fatalf("second claim body %q", c2.Snapshot.Body)
	}
}

func TestPromptFollowUpLeasedKeepsLease(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	_, pending, err := h.e.PromptFollowUp(sid, "steer")
	if err != nil || pending <= c.Job.ClaimedRevision {
		t.Fatalf("pending %d claimed %d %v", pending, c.Job.ClaimedRevision, err)
	}
	var state string
	if err := h.st.DB.QueryRow(`SELECT state FROM jobs WHERE id=?`, c.Job.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "leased" {
		t.Fatalf("state %s", state)
	}
}

func TestPromptSteerLiveUsesFencedHeartbeat(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	turnID, pending, live, err := h.e.PromptSteer(sid, "take the smaller fix")
	if err != nil || !live || turnID != c.Job.ID || pending != c.Job.ClaimedRevision {
		t.Fatalf("turn %d pending %d live %t err %v", turnID, pending, live, err)
	}
	secondID, _, secondLive, err := h.e.PromptSteer(sid, "also handle the edge case")
	if err != nil || !secondLive || secondID != turnID {
		t.Fatalf("second steer turn %d live %t err %v", secondID, secondLive, err)
	}
	steer, err := h.e.HeartbeatSteer(c.Job.ID, c.Job.LeaseGeneration+1, c.Job.ClaimedRevision)
	if err == nil || steer != nil {
		t.Fatalf("stale heartbeat returned steer %+v, err %v", steer, err)
	}
	steer, err = h.e.HeartbeatSteerReceived(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, nil)
	if err != nil || steer == nil || steer.Prompt != "take the smaller fix" {
		t.Fatalf("heartbeat steer %+v, err %v", steer, err)
	}
	retry, err := h.e.HeartbeatSteerReceived(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, nil)
	if err != nil || retry == nil || retry.ID != steer.ID {
		t.Fatalf("unconfirmed steer was not redelivered: %+v %v", retry, err)
	}
	second, err := h.e.HeartbeatSteerReceived(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, []int64{steer.ID})
	if err != nil || second == nil || second.ID == steer.ID || second.Prompt != "also handle the edge case" {
		t.Fatalf("second pending steer was not offered: %+v %v", second, err)
	}
	secondRetry, err := h.e.HeartbeatSteerReceived(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, []int64{steer.ID})
	if err != nil || secondRetry == nil || secondRetry.ID != second.ID {
		t.Fatalf("unconfirmed second steer was not redelivered: %+v %v", secondRetry, err)
	}
	third, err := h.e.HeartbeatSteerReceived(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, []int64{steer.ID, second.ID})
	if err != nil || third != nil {
		t.Fatalf("already offered steers repeated: %+v %v", third, err)
	}
	if _, err := h.e.CompleteWithSteers(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c), []int64{steer.ID, second.ID}); err != nil {
		t.Fatal(err)
	}
	var generation, revision int
	var state string
	var firstAck, firstPromoted, secondAck, secondPromoted int
	err = h.st.DB.QueryRow(`SELECT j.lease_generation, j.pending_revision, j.state,
 (SELECT acknowledged FROM turn_steers WHERE id=?), (SELECT promoted FROM turn_steers WHERE id=?),
 (SELECT acknowledged FROM turn_steers WHERE id=?), (SELECT promoted FROM turn_steers WHERE id=?)
 FROM jobs j WHERE j.id=?`, steer.ID, steer.ID, second.ID, second.ID, c.Job.ID).Scan(
		&generation, &revision, &state, &firstAck, &firstPromoted, &secondAck, &secondPromoted)
	if err != nil || generation != c.Job.LeaseGeneration || revision != c.Job.ClaimedRevision || state != "completed" || firstAck != 1 || firstPromoted != 0 || secondAck != 1 || secondPromoted != 0 {
		t.Fatalf("steer progress changed turn lease/revision: generation=%d revision=%d state=%s first=(%d,%d) second=(%d,%d) err=%v", generation, revision, state, firstAck, firstPromoted, secondAck, secondPromoted, err)
	}
}

func TestPromptSteerWithoutLiveTurnBecomesFollowUp(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	_, pending, live, err := h.e.PromptSteer(sid, "continue with the correction")
	if err != nil || live || pending != 2 {
		t.Fatalf("pending %d live %t err %v", pending, live, err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil || c.Snapshot.Body != "continue with the correction" {
		t.Fatalf("claim %+v err %v", c, err)
	}
	actions, err := store.ListActionsForSession(h.st, sid)
	if err != nil || len(actions) != 1 || actions[0].Type != "operator.steer" || !strings.Contains(actions[0].Body, `"actor":"operator"`) || !strings.Contains(actions[0].Body, `"delivery":"follow_up"`) {
		t.Fatalf("operator steer receipt %+v err %v", actions, err)
	}
}

func TestUnacknowledgedSteerActivatesAfterLeaseFails(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	if _, _, live, err := h.e.PromptSteer(sid, "recover this steer"); err != nil || !live {
		t.Fatalf("live %t err %v", live, err)
	}
	if _, err := h.e.Fail(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision); err != nil {
		t.Fatal(err)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil || c2.Snapshot.Body != "recover this steer" {
		t.Fatalf("promoted follow-up claim %+v %v", c2, err)
	}
}

func TestUnacknowledgedSteerActivatesAfterLeaseExpires(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	if _, _, live, err := h.e.PromptSteer(sid, "recover after expiry"); err != nil || !live {
		t.Fatalf("live %t err %v", live, err)
	}
	h.clk.T = c.Job.LeaseExpiresAt.Add(time.Second)
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil || c2.Snapshot.Body != "recover after expiry" {
		t.Fatalf("expired lease follow-up claim %+v %v", c2, err)
	}
}

func TestUnacknowledgedSteerFollowsExistingPendingRevision(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	if _, _, err := h.e.PromptFollowUp(sid, "existing follow-up"); err != nil {
		t.Fatal(err)
	}
	if _, _, live, err := h.e.PromptSteer(sid, "later steer"); err != nil || !live {
		t.Fatalf("live %t err %v", live, err)
	}
	if _, err := h.e.Fail(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision); err != nil {
		t.Fatal(err)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil || c2.Snapshot.Body != "existing follow-up" {
		t.Fatalf("existing pending revision was not preserved: %+v %v", c2, err)
	}
	if _, err := h.e.Complete(c2.Job.ID, c2.Job.LeaseGeneration, c2.Job.ClaimedRevision, runArt(c2)); err != nil {
		t.Fatal(err)
	}
	c3, err := h.e.Claim("example/test-repo")
	if err != nil || c3 == nil || c3.Snapshot.Body != "later steer" {
		t.Fatalf("promoted steer did not follow the existing revision: %+v %v", c3, err)
	}
}

func TestUnacknowledgedSteerStaysAheadOfLaterFollowUp(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	if _, _, live, err := h.e.PromptSteer(sid, "earlier live steer"); err != nil || !live {
		t.Fatalf("live %t err %v", live, err)
	}
	_, pending, err := h.e.PromptFollowUp(sid, "later ordinary follow-up")
	if err != nil || pending != c.Job.ClaimedRevision {
		t.Fatalf("later follow-up advanced ahead of unacknowledged steer: pending %d err %v", pending, err)
	}
	if _, err := h.e.Fail(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision); err != nil {
		t.Fatal(err)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil || c2.Snapshot.Body != "earlier live steer" {
		t.Fatalf("earlier steer was not claimed first: %+v %v", c2, err)
	}
	if _, err := h.e.Complete(c2.Job.ID, c2.Job.LeaseGeneration, c2.Job.ClaimedRevision, runArt(c2)); err != nil {
		t.Fatal(err)
	}
	c3, err := h.e.Claim("example/test-repo")
	if err != nil || c3 == nil || c3.Snapshot.Body != "later ordinary follow-up" {
		t.Fatalf("later follow-up was not claimed second: %+v %v", c3, err)
	}
}

func TestFollowUpHTTP(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", h.http.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/turns", strings.NewReader(`{"prompt":"next"}`))
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), "pending_revision") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	req, _ = http.NewRequest("POST", h.http.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/turns", strings.NewReader(`{"prompt":"x"}`))
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("unauth %d", res.StatusCode)
	}
}

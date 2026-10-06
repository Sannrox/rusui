package engine_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
)

// claimLeased starts a run and leases its first turn.
func claimLeased(t *testing.T, h *harn) (int64, *engine.Claim) {
	t.Helper()
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatalf("claim %+v %v", c, err)
	}
	return sid, c
}

func jobState(t *testing.T, h *harn, id int64) string {
	t.Helper()
	var state string
	if err := h.st.DB.QueryRow(`SELECT state FROM jobs WHERE id=?`, id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestQueuedPromptWaitsForCurrentTurn(t *testing.T) {
	h := setup(t)
	sid, c := claimLeased(t, h)
	turnID, pending, held, err := h.e.PromptQueued(sid, "after you finish")
	if err != nil || turnID != c.Job.ID || !held || pending != c.Job.ClaimedRevision {
		t.Fatalf("turn %d pending %d held %t err %v", turnID, pending, held, err)
	}
	steer, err := h.e.HeartbeatSteer(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision)
	if err != nil || steer != nil {
		t.Fatalf("queued prompt reached the live turn: %+v %v", steer, err)
	}
	if state := jobState(t, h, c.Job.ID); state != "leased" {
		t.Fatalf("state %s", state)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil || c2.Snapshot.Body != "after you finish" {
		t.Fatalf("queued prompt did not start after the turn ended: %+v %v", c2, err)
	}
}

func TestQueuedPromptsRunInOrder(t *testing.T) {
	h := setup(t)
	sid, c := claimLeased(t, h)
	for _, p := range []string{"one", "two"} {
		if _, _, held, err := h.e.PromptQueued(sid, p); err != nil || !held {
			t.Fatalf("queue %q held %t err %v", p, held, err)
		}
	}
	for _, want := range []string{"one", "two"} {
		if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
			t.Fatal(err)
		}
		var err error
		c, err = h.e.Claim("example/test-repo")
		if err != nil || c == nil || c.Snapshot.Body != want {
			t.Fatalf("want %q, claim %+v %v", want, c, err)
		}
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	if state := jobState(t, h, c.Job.ID); state != "completed" {
		t.Fatalf("state %s after the queue drained", state)
	}
}

func TestDropQueuedPromptsKeepsCurrentTurn(t *testing.T) {
	h := setup(t)
	sid, c := claimLeased(t, h)
	for _, p := range []string{"one", "two"} {
		if _, _, _, err := h.e.PromptQueued(sid, p); err != nil {
			t.Fatal(err)
		}
	}
	n, err := h.e.DropQueuedPrompts(sid)
	if err != nil || n != 2 {
		t.Fatalf("dropped %d err %v", n, err)
	}
	if err := h.e.Heartbeat(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision); err != nil {
		t.Fatalf("current turn did not keep running: %v", err)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	if state := jobState(t, h, c.Job.ID); state != "completed" {
		t.Fatalf("state %s", state)
	}
	if c2, err := h.e.Claim("example/test-repo"); err != nil || c2 != nil {
		t.Fatalf("dropped prompt started: %+v %v", c2, err)
	}
}

func TestCancelSessionDropsQueuedPrompts(t *testing.T) {
	h := setup(t)
	sid, c := claimLeased(t, h)
	if _, _, _, err := h.e.PromptQueued(sid, "never"); err != nil {
		t.Fatal(err)
	}
	if err := h.e.CancelSession(sid); err != nil {
		t.Fatal(err)
	}
	if state := jobState(t, h, c.Job.ID); state != "failed" {
		t.Fatalf("state %s", state)
	}
	if c2, err := h.e.Claim("example/test-repo"); err != nil || c2 != nil {
		t.Fatalf("queued prompt survived cancel: %+v %v", c2, err)
	}
}

func TestQueuedPromptOnEndedTurnStartsNow(t *testing.T) {
	h := setup(t)
	sid, c := claimLeased(t, h)
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	_, pending, held, err := h.e.PromptQueued(sid, "next")
	if err != nil || held || pending != c.Job.ClaimedRevision+1 {
		t.Fatalf("pending %d held %t err %v", pending, held, err)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil || c2.Snapshot.Body != "next" {
		t.Fatalf("claim %+v %v", c2, err)
	}
}

// An ordinary follow-up sent after a queued prompt waits behind it, and
// survives a drop of the queued prompts.
func TestFollowUpBehindQueuedPromptKeepsOrder(t *testing.T) {
	h := setup(t)
	sid, c := claimLeased(t, h)
	if _, _, _, err := h.e.PromptQueued(sid, "queued first"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := h.e.PromptQueued(sid, "dropped"); err != nil {
		t.Fatal(err)
	}
	_, pending, err := h.e.PromptFollowUp(sid, "follow-up second")
	if err != nil || pending != c.Job.ClaimedRevision {
		t.Fatalf("follow-up advanced ahead of the queued prompt: pending %d err %v", pending, err)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil || c2.Snapshot.Body != "queued first" {
		t.Fatalf("claim %+v %v", c2, err)
	}
	if n, err := h.e.DropQueuedPrompts(sid); err != nil || n != 1 {
		t.Fatalf("dropped %d err %v", n, err)
	}
	if _, err := h.e.Complete(c2.Job.ID, c2.Job.LeaseGeneration, c2.Job.ClaimedRevision, runArt(c2)); err != nil {
		t.Fatal(err)
	}
	c3, err := h.e.Claim("example/test-repo")
	if err != nil || c3 == nil || c3.Snapshot.Body != "follow-up second" {
		t.Fatalf("claim %+v %v", c3, err)
	}
}

func TestQueuedPromptHTTP(t *testing.T) {
	h := setup(t)
	sid, _ := claimLeased(t, h)
	base := h.http.URL + "/sessions/" + strconv.FormatInt(sid, 10)
	do := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer wsec")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		var out map[string]any
		_ = json.Unmarshal(b, &out)
		return res.StatusCode, out
	}
	if code, out := do("POST", "/turns", `{"prompt":"later","queued":true}`); code != 200 || out["delivery"] != "queued" || out["held"] != true {
		t.Fatalf("queue %d %v", code, out)
	}
	if code, _ := do("POST", "/turns", `{"prompt":"x","queued":true,"steer":true}`); code != 400 {
		t.Fatalf("steer and queued %d", code)
	}
	if code, out := do("DELETE", "/queued", ``); code != 200 || out["dropped"] != float64(1) {
		t.Fatalf("drop %d %v", code, out)
	}
	if code, _ := do("DELETE", "/queued", ``); code != 200 {
		t.Fatalf("empty drop %d", code)
	}
	req, _ := http.NewRequest("DELETE", base+"/queued", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("unauth %d", res.StatusCode)
	}
	req, _ = http.NewRequest("DELETE", h.http.URL+"/sessions/999999/queued", nil)
	req.Header.Set("Authorization", "Bearer wsec")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("missing session %d", res.StatusCode)
	}
}

// A retried failure is not terminal; the queued prompt waits until the
// revision exhausts its retries.
func TestQueuedPromptStartsAfterRetriesExhaust(t *testing.T) {
	h := setup(t)
	sid, c := claimLeased(t, h)
	if _, _, _, err := h.e.PromptQueued(sid, "after the failure"); err != nil {
		t.Fatal(err)
	}
	for i := range engine.RetryLimit {
		if i > 0 {
			var err error
			c, err = h.e.Claim("example/test-repo")
			if err != nil || c == nil || c.Snapshot.Body != "first" {
				t.Fatalf("retry %d claim %+v %v", i, c, err)
			}
		}
		if _, err := h.e.Fail(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision); err != nil {
			t.Fatal(err)
		}
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil || c.Snapshot.Body != "after the failure" {
		t.Fatalf("queued prompt did not start after the failed turn: %+v %v", c, err)
	}
}

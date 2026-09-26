package engine_test

import (
	"testing"

	"github.com/sannrox/rusui/internal/store"
)

func TestCancelSessionFailsLeasedWithoutRetry(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	if err := h.e.CancelSession(sid); err != nil {
		t.Fatal(err)
	}
	var state string
	var retries int
	if err := h.st.DB.QueryRow(`SELECT state, retry_count FROM jobs WHERE id=?`, c.Job.ID).Scan(&state, &retries); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || retries != 0 {
		t.Fatalf("state %s retries %d", state, retries)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err == nil {
		t.Fatal("stale complete after cancel must reject")
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil {
		t.Fatal(err)
	}
	if c2 != nil {
		t.Fatal("cancelled revision must not auto-reclaim")
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil || sess.ID != sid {
		t.Fatalf("session row %v %v", sess, err)
	}
}

func TestCancelSessionQueuesUnacknowledgedSteer(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	if _, _, live, err := h.e.PromptSteer(sid, "continue after cancellation"); err != nil || !live {
		t.Fatalf("live %t err %v", live, err)
	}
	if err := h.e.CancelSession(sid); err != nil {
		t.Fatal(err)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil || c2.Snapshot.Body != "continue after cancellation" {
		t.Fatalf("cancelled steer was not queued for follow-up: %+v %v", c2, err)
	}
	var promoted int
	if err := h.st.DB.QueryRow(`SELECT promoted FROM turn_steers WHERE turn_id=?`, c.Job.ID).Scan(&promoted); err != nil || promoted != 1 {
		t.Fatalf("steer promoted=%d err=%v", promoted, err)
	}
}

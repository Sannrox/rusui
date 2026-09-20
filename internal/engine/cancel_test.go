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

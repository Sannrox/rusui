package engine_test

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

func TestCancelQueuedTurnIsNotClaimable(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "queued work", "")
	if err != nil {
		t.Fatal(err)
	}
	var turnID int64
	var before string
	if err := h.st.DB.QueryRow(`SELECT id, state FROM turns WHERE session_id=?`, sid).Scan(&turnID, &before); err != nil {
		t.Fatal(err)
	}
	if before != "queued" {
		t.Fatalf("before %s", before)
	}
	req, err := http.NewRequest(http.MethodPost, h.http.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/cancel", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("cancel %d %s", res.StatusCode, body)
	}
	var state string
	if err := h.st.DB.QueryRow(`SELECT state FROM turns WHERE id=?`, turnID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "failed" {
		t.Fatalf("state %s", state)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil {
		t.Fatal(err)
	}
	if c != nil {
		t.Fatalf("cancelled queued turn was claimed: job %d", c.Job.ID)
	}
}

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

// A container session's cancel reaches the container driver's guest kill,
// not the process driver (#441).
func TestCancelContainerSessionKillsGuestProcesses(t *testing.T) {
	h := setup(t)
	rt := &env.FakeRuntime{}
	h.e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	box, err := h.e.ProvisionEnvironment(engine.EnvSpec{Name: "box-cancel", Kind: env.KindContainer})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionEnvironment(h.st, sid, box.ID); err != nil {
		t.Fatal(err)
	}
	if c, err := h.e.Claim("example/test-repo"); err != nil || c == nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil {
		t.Fatal(err)
	}
	cur, err := store.GetEnvironment(h.st, sess.EnvironmentID)
	if err != nil || cur.Driver != env.KindContainer || cur.Handle == "" {
		t.Fatalf("session environment %+v %v", cur, err)
	}
	if err := h.e.CancelSession(sid); err != nil {
		t.Fatal(err)
	}
	killed := false
	for _, ex := range rt.Execs {
		if ex[0] == cur.Handle && strings.Contains(strings.Join(ex, " "), "kill -TERM $l") {
			killed = true
		}
	}
	if !killed {
		t.Fatalf("guest of %s not killed: %#v", cur.Handle, rt.Execs)
	}
}

// While a cancel stops the guest, a turn the cancel requeued cannot be
// claimed onto that environment; it is claimable once the kill is done
// (#446).
func TestCancelHoldsEnvironmentUntilGuestIsStopped(t *testing.T) {
	h := setup(t)
	rt := &env.FakeRuntime{}
	h.e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	box, err := h.e.ProvisionEnvironment(engine.EnvSpec{Name: "box-race", Kind: env.KindContainer})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionEnvironment(h.st, sid, box.ID); err != nil {
		t.Fatal(err)
	}
	if c, err := h.e.Claim("example/test-repo"); err != nil || c == nil {
		t.Fatal(err)
	}
	if _, _, live, err := h.e.PromptSteer(sid, "continue after cancellation"); err != nil || !live {
		t.Fatalf("live %t err %v", live, err)
	}
	var duringKill *engine.Claim
	rt.ExecHook = func(id string, cmd []string) error {
		if strings.Contains(strings.Join(cmd, " "), "kill -TERM") {
			duringKill, err = h.e.Claim("example/test-repo")
			if err != nil {
				t.Error(err)
			}
		}
		return nil
	}
	if err := h.e.CancelSession(sid); err != nil {
		t.Fatal(err)
	}
	if duringKill != nil {
		t.Fatalf("requeued turn %d was claimed while its guest was being stopped", duringKill.Job.ID)
	}
	after, err := h.e.Claim("example/test-repo")
	if err != nil || after == nil {
		t.Fatalf("requeued turn not claimable after the cancel: %+v %v", after, err)
	}
}

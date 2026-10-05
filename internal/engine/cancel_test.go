package engine_test

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

// A cancel on a slept environment runs nothing in the guest: there is
// no harness to kill and no service to restart (#448).
func TestCancelSkipsGuestOfSleepingEnvironment(t *testing.T) {
	h := setup(t)
	rt := &env.FakeRuntime{}
	h.e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	box, err := h.e.ProvisionEnvironment(engine.EnvSpec{Name: "box-cancel-sleep", Kind: env.KindContainer})
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
	// The idle sleep stopped the container under the leased turn.
	cur, err := store.GetEnvironment(h.st, sess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	cur.State = store.EnvSleeping
	if err := store.UpdateEnvironment(h.st, *cur); err != nil {
		t.Fatal(err)
	}
	before := len(rt.Execs)
	if err := h.e.CancelSession(sid); err != nil {
		t.Fatal(err)
	}
	if extra := rt.Execs[before:]; len(extra) != 0 {
		t.Fatalf("cancel ran in a slept guest: %#v", extra)
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

// wakeHeldOpen sleeps the session's environment and starts a wake that holds
// the environment operation until release is closed, like a wake run by
// Claim for the turn the cancel is about to stop.
func wakeHeldOpen(t *testing.T, h *harn, rt *env.FakeRuntime, envID int64) (release chan struct{}, woke chan error) {
	t.Helper()
	// The idle sleep stopped the container under the leased turn.
	cur, err := store.GetEnvironment(h.st, envID)
	if err != nil {
		t.Fatal(err)
	}
	cur.State = store.EnvSleeping
	if err := store.UpdateEnvironment(h.st, *cur); err != nil {
		t.Fatal(err)
	}
	waking := make(chan struct{})
	release = make(chan struct{})
	rt.StartHook = func(string) error {
		close(waking)
		<-release
		return nil
	}
	woke = make(chan error, 1)
	go func() {
		_, err := h.e.WakeEnvironment(envID)
		woke <- err
	}()
	<-waking
	return release, woke
}

func claimOnContainer(t *testing.T, h *harn, rt *env.FakeRuntime, name string) (sid int64, box *store.Environment) {
	t.Helper()
	h.e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	box, err = h.e.ProvisionEnvironment(engine.EnvSpec{Name: name, Kind: env.KindContainer})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionEnvironment(h.st, sid, box.ID); err != nil {
		t.Fatal(err)
	}
	if c, err := h.e.Claim("example/test-repo"); err != nil || c == nil {
		t.Fatal(err)
	}
	// Claim may replace the environment for the turn's source.
	sess, err := store.GetSession(h.st, sid)
	if err != nil {
		t.Fatal(err)
	}
	box, err = store.GetEnvironment(h.st, sess.EnvironmentID)
	if err != nil || box.State != store.EnvReady || box.Handle == "" {
		t.Fatalf("session environment %+v %v", box, err)
	}
	return sid, box
}

func killedGuest(rt *env.FakeRuntime, handle string) bool {
	for _, ex := range rt.Execs {
		if ex[0] == handle && strings.Contains(strings.Join(ex, " "), "kill -TERM $l") {
			return true
		}
	}
	return false
}

// A cancel that waited out a claim-time wake judges the environment as it
// is once the wake is done, not as it was before: the guest is ready and
// is stopped (#472).
func TestCancelAfterClaimTimeWakeStopsGuest(t *testing.T) {
	h := setup(t)
	rt := &env.FakeRuntime{}
	sid, box := claimOnContainer(t, h, rt, "box-cancel-wake")
	release, woke := wakeHeldOpen(t, h, rt, box.ID)
	cancelled := make(chan error, 1)
	go func() { cancelled <- h.e.CancelSession(sid) }()
	// Let the cancel read the sleeping environment and queue behind the wake.
	time.Sleep(200 * time.Millisecond)
	close(release)
	if err := <-woke; err != nil {
		t.Fatal(err)
	}
	if err := <-cancelled; err != nil {
		t.Fatal(err)
	}
	cur, err := store.GetEnvironment(h.st, box.ID)
	if err != nil || cur.State != store.EnvReady {
		t.Fatalf("environment %+v %v", cur, err)
	}
	if !killedGuest(rt, cur.Handle) {
		t.Fatalf("guest of %s not killed after the wake: %#v", cur.Handle, rt.Execs)
	}
}

// A cancel never stops the guest without holding the environment
// operation. When another operation outlasts the wait, the cancel still
// commits, leaves the guest alone, and the runner's heartbeat ends the
// harness (#472).
func TestCancelDoesNotStopGuestWithoutHoldingEnvironment(t *testing.T) {
	h := setup(t)
	h.e.CancelEnvWait = 100 * time.Millisecond
	rt := &env.FakeRuntime{}
	sid, box := claimOnContainer(t, h, rt, "box-cancel-busy")
	var kills atomic.Int32
	killing := make(chan struct{})
	release := make(chan struct{})
	rt.ExecHook = func(_ string, cmd []string) error {
		if strings.Contains(strings.Join(cmd, " "), "kill -TERM") && kills.Add(1) == 1 {
			close(killing)
			<-release
		}
		return nil
	}
	// The first cancel holds the environment operation through its kill.
	first := make(chan error, 1)
	go func() { first <- h.e.CancelSession(sid) }()
	<-killing
	second := make(chan error, 1)
	go func() { second <- h.e.CancelSession(sid) }()
	select {
	case err := <-second:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(5 * time.Second):
		t.Error("second cancel did not return while the first held the environment")
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if n := kills.Load(); n != 1 {
		t.Fatalf("guest of %s killed %d times; a cancel killed without the environment operation", box.Handle, n)
	}
}

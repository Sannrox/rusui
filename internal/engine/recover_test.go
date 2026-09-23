package engine_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/store"
)

func TestRecoverExpiresOwnerBeforeStepRefresh(t *testing.T) {
	h := setup(t)
	it := issue(1)
	h.f.Put(it)
	if err := h.e.CatchUpItem(it.Repo, it.Item, it.ItemKind); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.DB.Exec(`UPDATE refresh_requests SET owner=1, state='running' WHERE repo=? AND item=?`, it.Repo, it.Item); err != nil {
		t.Fatal(err)
	}
	if err := h.e.Recover(); err != nil {
		t.Fatal(err)
	}
	var owner int
	var state string
	if err := h.st.DB.QueryRow(`SELECT owner, state FROM refresh_requests WHERE repo=? AND item=?`, it.Repo, it.Item).Scan(&owner, &state); err != nil {
		t.Fatal(err)
	}
	if owner != 0 || state != "queued" {
		t.Fatalf("owner=%d state=%s; Recover must requeue before StepRefresh", owner, state)
	}
	ok, err := h.e.StepRefresh()
	if err != nil || !ok {
		t.Fatalf("step after recover %v %v", ok, err)
	}
	j, _ := store.JobState(h.st, it.Repo, it.Item)
	if j == nil {
		t.Fatal("item stuck after startup expire")
	}
}

func TestRecoverInvalidatesRuntimeObservationsWithoutReleasingTurn(t *testing.T) {
	h := setup(t)
	runSessionID, err := h.e.StartRun("test", "keep the turn", "")
	if err != nil {
		t.Fatal(err)
	}
	var turnID int64
	if err := h.st.DB.QueryRow(`SELECT id FROM turns WHERE session_id=?`, runSessionID).Scan(&turnID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.DB.Exec(`UPDATE turns SET state='leased', lease_generation=7 WHERE id=?`, turnID); err != nil {
		t.Fatal(err)
	}
	res, err := h.st.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at)
		VALUES (?, ?, '', -1, 'local', 'open', ?)`, store.DefaultEnvironmentID, store.SessionKindLocal, h.clk.T.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	localSessionID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	process, err := store.StartSumikaProcess(h.st, localSessionID, h.clk.T)
	if err != nil {
		t.Fatal(err)
	}
	process, err = store.ObserveSumikaProcess(h.st, process.ID, process.Generation, process.Revision, store.ProcessRunning, h.clk.T.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	attach, err := store.BeginSumikaAttach(h.st, process.ID, process.Generation, h.clk.T.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}

	if err := h.e.Recover(); err != nil {
		t.Fatal(err)
	}
	process, err = store.GetSumikaProcess(h.st, process.ID)
	if err != nil || process.State != store.ProcessUnknown {
		t.Fatalf("process after recovery %+v %v", process, err)
	}
	if len(process.Attaches) != 1 || process.Attaches[0].ID != attach.ID || process.Attaches[0].State != store.AttachUnknown {
		t.Fatalf("attach after recovery %+v", process.Attaches)
	}
	var turnState string
	var generation int
	if err := h.st.DB.QueryRow(`SELECT state, lease_generation FROM turns WHERE id=?`, turnID).Scan(&turnState, &generation); err != nil {
		t.Fatal(err)
	}
	if turnState != "leased" || generation != 7 {
		t.Fatalf("runtime recovery changed turn: state=%s generation=%d", turnState, generation)
	}
}

func TestRecoverReconcilesMissingDelivery(t *testing.T) {
	h := setup(t)
	h.e.HookIDs = map[string]string{"example/test-repo": "1"}
	payload, err := json.Marshal(map[string]any{
		"repository": map[string]string{"full_name": "example/test-repo"},
		"issue":      map[string]int{"number": 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.f.Deliveries = []gh.DeliveryDetail{{
		ID: "missed-1", DeliveredAt: "2026-09-10T00:00:00Z", Event: "issues", Payload: payload,
	}}
	h.f.Put(issue(3))
	if err := h.e.Recover(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := h.st.DB.QueryRow(`SELECT COUNT(*) FROM deliveries WHERE delivery_id=?`, "missed-1").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("delivery rows %d", n)
	}
	if id := checkpointID(t, h.st, "example/test-repo"); id != "missed-1" {
		t.Fatalf("checkpoint %q", id)
	}
	ok, err := h.e.StepRefresh()
	if err != nil || !ok {
		t.Fatalf("step %v %v", ok, err)
	}
	j, _ := store.JobState(h.st, "example/test-repo", 3)
	if j == nil {
		t.Fatal("reconcile did not admit")
	}
}

func TestRecoverCatchUpClosedLocalItem(t *testing.T) {
	h := setup(t)
	it := issue(1)
	h.putRefresh(it)
	j, _ := store.JobState(h.st, it.Repo, it.Item)
	if j == nil {
		t.Fatal("setup")
		return
	}
	rev := j.PendingRevision
	it.State = "closed"
	it.UpdatedAt = "2026-09-11T00:00:00Z"
	h.f.Put(it)
	if err := h.e.Recover(); err != nil {
		t.Fatal(err)
	}
	ok, err := h.e.StepRefresh()
	if err != nil || !ok {
		t.Fatalf("step %v %v", ok, err)
	}
	j2, _ := store.JobState(h.st, it.Repo, it.Item)
	if j2.PendingRevision <= rev {
		t.Fatalf("pending still %d", j2.PendingRevision)
	}
	snap, err := store.LoadSnapshot(h.st, it.Repo, it.Item, j2.PendingRevision)
	if err != nil {
		t.Fatal(err)
	}
	if snap.State != "closed" {
		t.Fatalf("state %q", snap.State)
	}
}

func TestRecoverRetriesInFlightApply(t *testing.T) {
	h := setup(t)
	it := issue(1)
	it.CreatedAt = h.clk.T.Add(-90 * 24 * time.Hour).Format(time.RFC3339)
	it.LastNonBotCommentAt = it.CreatedAt
	h.putRefresh(it)
	c := h.claim()
	if err := h.e.SetPause("test", true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "propose_close", "close", "stale_insufficient_info")); err != nil {
		t.Fatal(err)
	}
	var revID int64
	if err := h.st.DB.QueryRow(`SELECT id FROM review_revisions WHERE job_id=?`, c.Job.ID).Scan(&revID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.DB.Exec(`INSERT INTO apply_attempts (action_id, review_revision_id, repo, item, state) VALUES (?,?,?,?, 'in_flight')`,
		"startup-uncertain", revID, it.Repo, it.Item); err != nil {
		t.Fatal(err)
	}
	if err := h.e.SetPause("test", false); err != nil {
		t.Fatal(err)
	}
	if err := h.e.Recover(); err != nil {
		t.Fatal(err)
	}
	n, _ := store.CountIntended(h.st, it.Repo, it.Item)
	if n != 1 {
		t.Fatalf("intended %d; startup must retry the same action_id", n)
	}
}

func TestRecoverSkipsReconcileWithoutHookIDs(t *testing.T) {
	h := setup(t)
	var notes []string
	h.e.Notify = func(msg string) { notes = append(notes, msg) }
	if err := h.e.Recover(); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range notes {
		if strings.Contains(n, "RUSUI_GITHUB_HOOK_IDS is unset") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected skip reason, got %v", notes)
	}
}

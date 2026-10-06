package engine_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

func TestStepSchedulesBucketsAndSkipLive(t *testing.T) {
	h := setup(t)
	id, err := h.e.CreateSchedule("test", "hourly", "1m", "scheduled work", 0)
	if err != nil || id == 0 {
		t.Fatalf("%d %v", id, err)
	}
	t0 := time.Unix(1_700_000_000, 0).UTC()
	if err := h.e.StepSchedules(t0); err != nil {
		t.Fatal(err)
	}
	if err := h.e.StepSchedules(t0.Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	n := countKind(t, h.st, store.SessionKindScheduled)
	if n != 1 {
		t.Fatalf("same bucket %d", n)
	}
	if err := h.e.StepSchedules(t0.Add(61 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if countKind(t, h.st, store.SessionKindScheduled) != 1 {
		t.Fatal("live session should skip")
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil || c.Job.Lane != "scheduled" {
		t.Fatalf("claim %+v %v", c, err)
	}
}

func TestScheduleFireAfterCompleteMintsNewSession(t *testing.T) {
	h := setup(t)
	id, err := h.e.CreateSchedule("test", "hourly", "1m", "scheduled work", 0)
	if err != nil || id == 0 {
		t.Fatalf("%d %v", id, err)
	}
	t0 := time.Unix(1_700_000_000, 0).UTC()
	if err := h.e.StepSchedules(t0); err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	turn, err := store.GetTurn(h.st, c.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	first := turn.SessionID
	if err := h.e.CancelSession(first); err != nil {
		t.Fatal(err)
	}
	if err := h.e.StepSchedules(t0.Add(61 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if n := countKind(t, h.st, store.SessionKindScheduled); n != 2 {
		t.Fatalf("after complete fire %d sessions", n)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil {
		t.Fatalf("second fire claim %+v %v", c2, err)
	}
	turn2, err := store.GetTurn(h.st, c2.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turn2.SessionID == first {
		t.Fatal("fire continued the old session")
	}
}

const twoProjectFixture = `version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
  review: true
  comments: true
  close: true
  implement: false
  land: false
  max_reviews_per_repo_per_utc_day: 50
projects:
  test:
    repos:
      example/test-repo:
        visibility: public
        review: true
        comments: true
        close: true
        implement: false
        land: false
  test2:
    repos:
      example/test-repo-2:
        visibility: public
        review: true
        comments: true
        close: true
        implement: false
        land: false
`

// A schedule whose project StartScheduled refuses must not block schedules
// listed after it from starting, and the refusal must still be reported.
func TestStepSchedulesContinuesPastRefusalAndReportsIt(t *testing.T) {
	h := setup(t)
	twoProjects, err := policy.Parse([]byte(twoProjectFixture))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(twoProjects)

	badID, err := h.e.CreateSchedule("test", "will-be-refused", "1m", "scheduled work", 0)
	if err != nil || badID == 0 {
		t.Fatalf("%d %v", badID, err)
	}
	goodID, err := h.e.CreateSchedule("test2", "stays-healthy", "1m", "scheduled work", 0)
	if err != nil || goodID == 0 {
		t.Fatalf("%d %v", goodID, err)
	}

	// Drop "test" from policy after the schedule exists, so StartScheduled
	// refuses it on the next step while "test2" remains healthy.
	onlyTest2, err := policy.Parse([]byte(`version: 2
defaults:
  session_kinds: [review, run, scheduled]
  egress: trusted
projects:
  test2:
    repos:
      example/test-repo-2:
        visibility: public
`))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(onlyTest2)

	t0 := time.Unix(1_700_000_000, 0).UTC()
	if err := h.e.StepSchedules(t0); err == nil {
		t.Fatal("expected refusal to be reported")
	}
	if n := countKind(t, h.st, store.SessionKindScheduled); n != 1 {
		t.Fatalf("healthy schedule after the refused one should still start, got %d", n)
	}
}

func TestCreateScheduleRequiresKind(t *testing.T) {
	h := setup(t)
	if _, err := h.e.CreateSchedule("missing", "n", "1m", "p", 0); err == nil {
		t.Fatal("expected policy error")
	}
}

func TestBoundScheduleFireQueuesSameSessionAndEnvironment(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.CreateSchedule("test", "watch", "1m", "scheduled work", sid); err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	if c.Snapshot.Body != "first" {
		t.Fatalf("fire steered the live turn: %q", c.Snapshot.Body)
	}
	t0 := time.Unix(1_700_000_000, 0).UTC()
	if err := h.e.StepSchedules(t0); err != nil {
		t.Fatal(err)
	}
	steer, err := h.e.HeartbeatSteer(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision)
	if err != nil || steer != nil {
		t.Fatalf("bound fire steered: %+v %v", steer, err)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil || c2.Snapshot.Body != "scheduled work" {
		t.Fatalf("bound fire did not start after the turn: %+v %v", c2, err)
	}
	turn, err := store.GetTurn(h.st, c2.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turn.SessionID != sid {
		t.Fatalf("session %d want %d", turn.SessionID, sid)
	}
	sess2, err := store.GetSession(h.st, sid)
	if err != nil {
		t.Fatal(err)
	}
	if sess2.EnvironmentID != sess.EnvironmentID {
		t.Fatalf("environment %d want %d", sess2.EnvironmentID, sess.EnvironmentID)
	}
	if n := countKind(t, h.st, store.SessionKindScheduled); n != 0 {
		t.Fatalf("bound fire minted a scheduled session: %d", n)
	}
}

func TestBoundScheduleSameBucketDoesNotDoubleQueue(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.CreateSchedule("test", "watch", "1m", "scheduled work", sid); err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	t0 := time.Unix(1_700_000_000, 0).UTC()
	if err := h.e.StepSchedules(t0); err != nil {
		t.Fatal(err)
	}
	if err := h.e.StepSchedules(t0.Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := h.st.DB.QueryRow(`SELECT COUNT(*) FROM followup_queue WHERE session_id=? AND consumed=0`, sid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("same bucket queued %d prompts", n)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil || c2.Snapshot.Body != "scheduled work" {
		t.Fatalf("claim %+v %v", c2, err)
	}
	if _, err := h.e.Complete(c2.Job.ID, c2.Job.LeaseGeneration, c2.Job.ClaimedRevision, runArt(c2)); err != nil {
		t.Fatal(err)
	}
	if c3, err := h.e.Claim("example/test-repo"); err != nil || c3 != nil {
		t.Fatalf("double-queued prompt started: %+v %v", c3, err)
	}
}

func TestDeleteScheduleStopsLaterFiresAndKeepsTurn(t *testing.T) {
	h := setup(t)
	id, err := h.e.CreateSchedule("test", "hourly", "1m", "scheduled work", 0)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1_700_000_000, 0).UTC()
	if err := h.e.StepSchedules(t0); err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	if err := h.e.DeleteSchedule("test", id); err != nil {
		t.Fatal(err)
	}
	if err := h.e.Heartbeat(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision); err != nil {
		t.Fatalf("delete cancelled the running turn: %v", err)
	}
	if err := h.e.StepSchedules(t0.Add(61 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if n := countKind(t, h.st, store.SessionKindScheduled); n != 1 {
		t.Fatalf("later fire after delete %d sessions", n)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	if c2, err := h.e.Claim("example/test-repo"); err != nil || c2 != nil {
		t.Fatalf("deleted schedule still fired: %+v %v", c2, err)
	}
}

func TestCreateScheduleBoundSessionChecks(t *testing.T) {
	h := setup(t)
	if _, err := h.e.CreateSchedule("test", "missing", "1m", "p", 999); err == nil {
		t.Fatal("expected missing session error")
	}
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.e.ArchiveSession(sid); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.CreateSchedule("test", "archived", "1m", "p", sid); err == nil {
		t.Fatal("expected archived session error")
	}
	it := issue(42)
	h.putRefresh(it)
	var reviewID int64
	if err := h.st.DB.QueryRow(`SELECT id FROM sessions WHERE kind=?`, store.SessionKindReview).Scan(&reviewID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.CreateSchedule("test", "review", "1m", "p", reviewID); err == nil {
		t.Fatal("expected kind error")
	}
}

func TestBoundScheduleArchivedFireDoesNotQueue(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.CreateSchedule("test", "watch", "1m", "scheduled work", sid); err != nil {
		t.Fatal(err)
	}
	if err := h.e.ArchiveSession(sid); err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1_700_000_000, 0).UTC()
	if err := h.e.StepSchedules(t0); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := h.st.DB.QueryRow(`SELECT COUNT(*) FROM followup_queue WHERE session_id=? AND consumed=0`, sid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("archived bound fire queued %d prompts", n)
	}
}

func TestBoundScheduleHTTPCreateAndDelete(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, h.http.URL+path, strings.NewReader(body))
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
	code, out := do("POST", "/projects/test/schedules", `{"name":"watch","every":"1m","prompt":"scheduled work","session_id":`+strconv.FormatInt(sid, 10)+`}`)
	if code != 201 || out["session_id"] != float64(sid) {
		t.Fatalf("create bound %d %v", code, out)
	}
	id := int64(out["id"].(float64))
	code, _ = do("DELETE", "/projects/test/schedules/"+strconv.FormatInt(id, 10), "")
	if code != 204 {
		t.Fatalf("delete %d", code)
	}
	code, _ = do("DELETE", "/projects/test/schedules/"+strconv.FormatInt(id, 10), "")
	if code != 404 {
		t.Fatalf("missing delete %d", code)
	}
	req, _ := http.NewRequest("DELETE", h.http.URL+"/projects/test/schedules/1", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("unauth %d", res.StatusCode)
	}
}

func countKind(t *testing.T, st *store.Store, kind string) int {
	t.Helper()
	var n int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE kind=?`, kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestGuestScheduleSetFiresSameSession(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	rec, err := h.e.GuestScheduleRequest(c.Job.ID, engine.ScheduleRequest{Op: "set", Every: "1m", Prompt: "scheduled work"})
	if err != nil || !rec.Accepted || rec.SessionID != sid || rec.ScheduleID == 0 {
		t.Fatalf("set %+v %v", rec, err)
	}
	if rec.ID == "" {
		t.Fatal("receipt id")
	}
	list, err := store.ListSchedules(h.st)
	if err != nil || len(list) != 1 || list[0].SessionID != sid || list[0].Prompt != "scheduled work" {
		t.Fatalf("bound schedule %+v %v", list, err)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1_700_000_000, 0).UTC()
	if err := h.e.StepSchedules(t0); err != nil {
		t.Fatal(err)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil || c2.Snapshot.Body != "scheduled work" {
		t.Fatalf("fire %+v %v", c2, err)
	}
	turn, err := store.GetTurn(h.st, c2.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turn.SessionID != sid {
		t.Fatalf("session %d want %d", turn.SessionID, sid)
	}
	sess2, err := store.GetSession(h.st, sid)
	if err != nil {
		t.Fatal(err)
	}
	if sess2.EnvironmentID != sess.EnvironmentID {
		t.Fatalf("environment %d want %d", sess2.EnvironmentID, sess.EnvironmentID)
	}
	acts, err := store.ListActionsForSession(h.st, sid)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range acts {
		if a.Type == "schedule.request" && a.ReasonCode == "accepted" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing accepted receipt")
	}
}

func TestGuestScheduleReplaceUpdatesPrompt(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	if _, err := h.e.GuestScheduleRequest(c.Job.ID, engine.ScheduleRequest{Op: "set", Every: "1m", Prompt: "old"}); err != nil {
		t.Fatal(err)
	}
	rec, err := h.e.GuestScheduleRequest(c.Job.ID, engine.ScheduleRequest{Op: "replace", Every: "1m", Prompt: "new"})
	if err != nil || !rec.Accepted {
		t.Fatalf("replace %+v %v", rec, err)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1_700_000_000, 0).UTC()
	if err := h.e.StepSchedules(t0); err != nil {
		t.Fatal(err)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil || c2.Snapshot.Body != "new" {
		t.Fatalf("fire %+v %v", c2, err)
	}
	turn, err := store.GetTurn(h.st, c2.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turn.SessionID != sid {
		t.Fatalf("session %d want %d", turn.SessionID, sid)
	}
}

func TestGuestScheduleClearStopsLaterFires(t *testing.T) {
	h := setup(t)
	if _, err := h.e.StartRun("test", "first", ""); err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	if _, err := h.e.GuestScheduleRequest(c.Job.ID, engine.ScheduleRequest{Op: "set", Every: "1m", Prompt: "scheduled work"}); err != nil {
		t.Fatal(err)
	}
	rec, err := h.e.GuestScheduleRequest(c.Job.ID, engine.ScheduleRequest{Op: "clear"})
	if err != nil || !rec.Accepted {
		t.Fatalf("clear %+v %v", rec, err)
	}
	list, err := store.ListSchedules(h.st)
	if err != nil || len(list) != 0 {
		t.Fatalf("cleared %+v %v", list, err)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1_700_000_000, 0).UTC()
	if err := h.e.StepSchedules(t0); err != nil {
		t.Fatal(err)
	}
	if c2, err := h.e.Claim("example/test-repo"); err != nil || c2 != nil {
		t.Fatalf("cleared schedule fired: %+v %v", c2, err)
	}
}

func TestGuestScheduleRefusalLeavesPrevious(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := h.e.StartRun("test", "other", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.e.CancelSession(other); err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	if _, err := h.e.GuestScheduleRequest(c.Job.ID, engine.ScheduleRequest{Op: "set", Every: "1m", Prompt: "keep me"}); err != nil {
		t.Fatal(err)
	}
	rec, err := h.e.GuestScheduleRequest(c.Job.ID, engine.ScheduleRequest{Op: "set", SessionID: other, Every: "1m", Prompt: "steal"})
	if err != nil || rec.Accepted || rec.Detail != "another session" {
		t.Fatalf("refuse %+v %v", rec, err)
	}
	list, err := store.ListSchedules(h.st)
	if err != nil || len(list) != 1 || list[0].Prompt != "keep me" || list[0].SessionID != sid {
		t.Fatalf("previous %+v %v", list, err)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1_700_000_000, 0).UTC()
	if err := h.e.StepSchedules(t0); err != nil {
		t.Fatal(err)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil || c2.Snapshot.Body != "keep me" {
		t.Fatalf("previous fire %+v %v", c2, err)
	}
	turn, err := store.GetTurn(h.st, c2.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turn.SessionID != sid {
		t.Fatalf("session %d want %d", turn.SessionID, sid)
	}
}

func TestGuestScheduleAnotherProjectRefuse(t *testing.T) {
	h := setup(t)
	two, err := policy.Parse([]byte(twoProjectFixture))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(two)
	if _, err := h.e.StartRun("test", "first", ""); err != nil {
		t.Fatal(err)
	}
	other, err := h.e.StartRun("test2", "other", "")
	if err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	rec, err := h.e.GuestScheduleRequest(c.Job.ID, engine.ScheduleRequest{Op: "set", SessionID: other, Every: "1m", Prompt: "x"})
	if err != nil || rec.Accepted || rec.Detail != "another project" {
		t.Fatalf("project %+v %v", rec, err)
	}
	list, err := store.ListSchedules(h.st)
	if err != nil || len(list) != 0 {
		t.Fatalf("schedules %+v %v", list, err)
	}
}

func TestGuestScheduleKindRefuse(t *testing.T) {
	h := setup(t)
	it := issue(42)
	h.putRefresh(it)
	c := h.claim()
	if c.Job.Lane != "review" {
		t.Fatalf("lane %s", c.Job.Lane)
	}
	rec, err := h.e.GuestScheduleRequest(c.Job.ID, engine.ScheduleRequest{Op: "set", Every: "1m", Prompt: "no"})
	if err != nil || rec.Accepted || rec.Detail != "kind" {
		t.Fatalf("kind %+v %v", rec, err)
	}
	list, err := store.ListSchedules(h.st)
	if err != nil || len(list) != 0 {
		t.Fatalf("schedules %+v %v", list, err)
	}
}

func TestGuestScheduleHTTPAuth(t *testing.T) {
	h := setup(t)
	if _, err := h.e.StartRun("test", "first", ""); err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	do := func(auth, body string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest("POST", h.http.URL+"/turns/"+strconv.FormatInt(c.Job.ID, 10)+"/schedule", strings.NewReader(body))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
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
	code, _ := do("", `{"op":"set","every":"1m","prompt":"scheduled work"}`)
	if code != 401 {
		t.Fatalf("unauth %d", code)
	}
	code, out := do("Bearer wsec", `{"op":"set","every":"1m","prompt":"scheduled work"}`)
	if code != 202 || out["accepted"] != true {
		t.Fatalf("worker %d %v", code, out)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	code, _ = do("Bearer wsec", `{"op":"clear"}`)
	if code != 409 {
		t.Fatalf("unleased %d", code)
	}
}

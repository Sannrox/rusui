package engine_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

const guestProjectYAML = `
  guest:
    session_kinds: [run]
    repos:
      example/guest-repo:
        visibility: public
        review: true
        comments: false
        close: false
        implement: false
        land: false
`

func TestChildSessionOwnEnvironmentAndReportsBack(t *testing.T) {
	h := setup(t)
	pol, err := policy.Parse([]byte(strings.Replace(fixture, "test:\n", "test:\n    budgets: {max_concurrent_leases: 2}\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(pol)
	parent, err := h.e.StartRun("test", "investigate", "")
	if err != nil {
		t.Fatal(err)
	}
	child, err := h.e.StartChild(parent, "look at tests", "")
	if err != nil {
		t.Fatal(err)
	}
	ps, err := store.GetSession(h.st, parent)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := store.GetSession(h.st, child)
	if err != nil {
		t.Fatal(err)
	}
	if cs.ParentSessionID != parent {
		t.Fatalf("parent %d", cs.ParentSessionID)
	}
	if cs.EnvironmentID == 0 || cs.EnvironmentID == ps.EnvironmentID {
		t.Fatalf("child environment %d parent %d", cs.EnvironmentID, ps.EnvironmentID)
	}
	if cs.Project != ps.Project || cs.Repo != ps.Repo || cs.Kind != store.SessionKindRun {
		t.Fatalf("child %+v parent %+v", cs, ps)
	}
	if _, err := h.e.StartChild(child, "deeper", ""); err == nil || err.Error() != "depth" {
		t.Fatalf("depth %v", err)
	}
	acts, err := store.ListActionsForSession(h.st, parent)
	if err != nil {
		t.Fatal(err)
	}
	spawned := false
	for _, a := range acts {
		if a.Type == "child.spawn" && strings.Contains(a.Body, strconv.FormatInt(child, 10)) {
			spawned = true
		}
	}
	if !spawned {
		t.Fatalf("spawn actions %+v", acts)
	}
	c1 := h.claim()
	if c1.Job.Item == cs.Item {
		t.Fatal("parent should claim first")
	}
	if _, err := h.e.Complete(c1.Job.ID, c1.Job.LeaseGeneration, c1.Job.ClaimedRevision, runArt(c1)); err != nil {
		t.Fatal(err)
	}
	c2 := h.claim()
	if c2.Job.Item != cs.Item {
		t.Fatalf("child claim item %d want %d", c2.Job.Item, cs.Item)
	}
	if _, err := h.e.Complete(c2.Job.ID, c2.Job.LeaseGeneration, c2.Job.ClaimedRevision, runArt(c2)); err != nil {
		t.Fatal(err)
	}
	acts, err = store.ListActionsForSession(h.st, parent)
	if err != nil {
		t.Fatal(err)
	}
	reported := false
	for _, a := range acts {
		if a.Type == "child.result" && strings.Contains(a.Body, `"outcome":"completed"`) {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("result actions %+v", acts)
	}
}

func TestChildOptionalGuestBindsAndRefusesUnlisted(t *testing.T) {
	h := setup(t)
	parent, err := h.e.StartRun("test", "parent", "")
	if err != nil {
		t.Fatal(err)
	}
	child, err := h.e.StartChildGuest(parent, "child", "", "grok")
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.GetSession(h.st, child)
	if err != nil {
		t.Fatal(err)
	}
	if s.GuestName != "grok" || s.GuestPin == "" {
		t.Fatalf("child guest %+v", s)
	}
	before, err := store.ListSessions(h.st, "test", 50)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.StartChildGuest(parent, "other", "", "shikigami"); err == nil {
		t.Fatal("unlisted child guest accepted")
	}
	after, err := store.ListSessions(h.st, "test", 50)
	if err != nil || len(after) != len(before) {
		t.Fatalf("refused child stored: %d -> %d %v", len(before), len(after), err)
	}
}

func TestChildCancelCascadesAndFanOut(t *testing.T) {
	h := setup(t)
	parent, err := h.e.StartRun("test", "investigate", "")
	if err != nil {
		t.Fatal(err)
	}
	child, err := h.e.StartChild(parent, "look", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.StartChild(parent, "again", ""); err == nil || err.Error() != "fan-out" {
		t.Fatalf("fan-out %v", err)
	}
	if err := h.e.CancelSession(parent); err != nil {
		t.Fatal(err)
	}
	turns, err := store.ListTurnsForSession(h.st, child)
	if err != nil || len(turns) == 0 {
		t.Fatalf("turns %v %v", turns, err)
	}
	if turns[0].State != "failed" {
		t.Fatalf("child turn %s", turns[0].State)
	}
}

func TestChildHTTP(t *testing.T) {
	h := setup(t)
	parent, err := h.e.StartRun("test", "investigate", "")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", h.http.URL+"/sessions/"+strconv.FormatInt(parent, 10)+"/children", strings.NewReader(`{"prompt":"look"}`))
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, b)
	}
	var out struct {
		SessionID int64 `json:"session_id"`
	}
	if err := json.Unmarshal(b, &out); err != nil || out.SessionID == 0 {
		t.Fatalf("body %s %v", b, err)
	}
	req, _ = http.NewRequest("POST", h.http.URL+"/sessions/"+strconv.FormatInt(out.SessionID, 10)+"/children", strings.NewReader(`{"prompt":"deeper"}`))
	req.Header.Set("Authorization", "Bearer wsec")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 400 || !strings.Contains(string(b), "depth") {
		t.Fatalf("depth %d %s", res.StatusCode, b)
	}
}

func twoProjectChild(t *testing.T) *harn {
	t.Helper()
	h := setup(t)
	raw := strings.Replace(fixture, "test:\n", "test:\n    budgets: {max_concurrent_leases: 2}\n", 1) + guestProjectYAML
	pol, err := policy.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(pol)
	h.f.Put(snapshot.Item{Repo: "example/guest-repo", Item: 1, ItemKind: "issue", State: "open", MainSHA: "bbb", DefaultBranch: "main"})
	return h
}

func TestChildCrossProjectUsesNamedPolicy(t *testing.T) {
	h := twoProjectChild(t)
	parent, err := h.e.StartRun("test", "pin guest protocol", "")
	if err != nil {
		t.Fatal(err)
	}
	child, err := h.e.StartChild(parent, "document the pin", "guest")
	if err != nil {
		t.Fatal(err)
	}
	ps, err := store.GetSession(h.st, parent)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := store.GetSession(h.st, child)
	if err != nil {
		t.Fatal(err)
	}
	if cs.Project != "guest" || cs.Repo != "example/guest-repo" || cs.ParentSessionID != parent {
		t.Fatalf("child %+v", cs)
	}
	if cs.Project == ps.Project || cs.Repo == ps.Repo || cs.EnvironmentID == ps.EnvironmentID {
		t.Fatalf("child shared parent identity %+v %+v", cs, ps)
	}
	snap, err := store.LoadSnapshot(h.st, cs.Repo, cs.Item, 1)
	if err != nil || snap.MainSHA != "bbb" || snap.Body != "document the pin" {
		t.Fatalf("child snapshot %+v %v", snap, err)
	}
	parentSnap, err := store.LoadSnapshot(h.st, ps.Repo, ps.Item, 1)
	if err != nil || parentSnap.MainSHA == snap.MainSHA {
		t.Fatalf("parent snapshot %+v child %+v %v", parentSnap, snap, err)
	}
	task, ok := h.e.ImplementTask(cs)
	if ok || task != nil {
		t.Fatalf("child inherited implement %+v", task)
	}
	acts, err := store.ListActionsForSession(h.st, parent)
	if err != nil {
		t.Fatal(err)
	}
	spawned := false
	for _, a := range acts {
		if a.Type == "child.spawn" && strings.Contains(a.Body, `"project":"guest"`) && a.Repo == ps.Repo {
			spawned = true
		}
	}
	if !spawned {
		t.Fatalf("spawn actions %+v", acts)
	}
	c1 := h.claim()
	if c1.Job.Repo != ps.Repo || c1.Job.Item != ps.Item {
		t.Fatalf("parent claim %+v want %s #%d", c1.Job, ps.Repo, ps.Item)
	}
	if _, err := h.e.Complete(c1.Job.ID, c1.Job.LeaseGeneration, c1.Job.ClaimedRevision, runArt(c1)); err != nil {
		t.Fatal(err)
	}
	c2, err := h.e.Claim("example/guest-repo")
	if err != nil || c2 == nil || c2.Job.Repo != cs.Repo || c2.Job.Item != cs.Item {
		t.Fatalf("child claim %+v %v", c2, err)
	}
	if _, err := h.e.Complete(c2.Job.ID, c2.Job.LeaseGeneration, c2.Job.ClaimedRevision, runArt(c2)); err != nil {
		t.Fatal(err)
	}
	acts, err = store.ListActionsForSession(h.st, parent)
	if err != nil {
		t.Fatal(err)
	}
	reported := false
	for _, a := range acts {
		if a.Type == "child.result" && strings.Contains(a.Body, `"outcome":"completed"`) && strings.Contains(a.Body, `"project":"guest"`) && a.Repo == ps.Repo && a.Item == ps.Item {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("result actions %+v", acts)
	}
}

func TestChildUnknownProjectAndDeniedKind(t *testing.T) {
	h := twoProjectChild(t)
	parent, err := h.e.StartRun("test", "investigate", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.StartChild(parent, "look", "missing"); err == nil || err.Error() != "policy" {
		t.Fatalf("missing %v", err)
	}
	raw := strings.Replace(fixture, "test:\n", "test:\n    budgets: {max_concurrent_leases: 2}\n", 1) + `
  guest:
    session_kinds: [review]
    repos:
      example/guest-repo:
        visibility: public
        review: true
`
	pol, err := policy.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(pol)
	if _, err := h.e.StartChild(parent, "look", "guest"); err == nil || err.Error() != "policy" {
		t.Fatalf("kind %v", err)
	}
}

func TestChildPausedTargetAndCancel(t *testing.T) {
	h := twoProjectChild(t)
	parent, err := h.e.StartRun("test", "investigate", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.st.Tx(func(tx *sql.Tx) error {
		return store.OverlaySet(tx, "pause:guest", "1")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.StartChild(parent, "look", "guest"); err == nil || err.Error() != "paused" {
		t.Fatalf("paused %v", err)
	}
	if err := h.st.Tx(func(tx *sql.Tx) error {
		return store.OverlaySet(tx, "pause:guest", "0")
	}); err != nil {
		t.Fatal(err)
	}
	child, err := h.e.StartChild(parent, "look", "guest")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.e.CancelSession(parent); err != nil {
		t.Fatal(err)
	}
	turns, err := store.ListTurnsForSession(h.st, child)
	if err != nil || len(turns) == 0 || turns[0].State != "failed" {
		t.Fatalf("child turn %v %v", turns, err)
	}
}

func TestChildCrossProjectHTTPAndPartialComplete(t *testing.T) {
	h := twoProjectChild(t)
	parent, err := h.e.StartRun("test", "investigate", "")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", h.http.URL+"/sessions/"+strconv.FormatInt(parent, 10)+"/children", strings.NewReader(`{"prompt":"look","project":"guest"}`))
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, b)
	}
	var out struct {
		SessionID int64 `json:"session_id"`
	}
	if err := json.Unmarshal(b, &out); err != nil || out.SessionID == 0 {
		t.Fatalf("body %s %v", b, err)
	}
	cs, err := store.GetSession(h.st, out.SessionID)
	if err != nil || cs.Project != "guest" {
		t.Fatalf("child %+v %v", cs, err)
	}
	c1 := h.claim()
	if c1.Job.Repo != "example/test-repo" {
		t.Fatal("parent complete claimed the child")
	}
	if _, err := h.e.Complete(c1.Job.ID, c1.Job.LeaseGeneration, c1.Job.ClaimedRevision, runArt(c1)); err != nil {
		t.Fatal(err)
	}
	turns, err := store.ListTurnsForSession(h.st, cs.ID)
	if err != nil || len(turns) == 0 || turns[0].State != "queued" {
		t.Fatalf("child still queued %v %v", turns, err)
	}
	req, _ = http.NewRequest("POST", h.http.URL+"/sessions/"+strconv.FormatInt(parent, 10)+"/children", strings.NewReader(`{"prompt":"look","project":"missing"}`))
	req.Header.Set("Authorization", "Bearer wsec")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict || !strings.Contains(string(b), "policy") {
		t.Fatalf("missing %d %s", res.StatusCode, b)
	}
}

func TestChildSameProjectKeepsParentRepo(t *testing.T) {
	h := setup(t)
	parent, err := h.e.StartRun("test", "investigate", "")
	if err != nil {
		t.Fatal(err)
	}
	ps, err := store.GetSession(h.st, parent)
	if err != nil {
		t.Fatal(err)
	}
	h.f.Put(snapshot.Item{Repo: "example/other-repo", Item: 1, ItemKind: "issue", State: "open", MainSHA: "ccc", DefaultBranch: "main"})
	raw := strings.Replace(fixture, "example/test-repo:", "example/other-repo:\n        visibility: public\n        review: true\n        comments: true\n        close: true\n        implement: false\n        land: false\n      example/test-repo:", 1)
	pol, err := policy.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(pol)
	child, err := h.e.StartChild(parent, "look", "")
	if err != nil {
		t.Fatal(err)
	}
	cs, err := store.GetSession(h.st, child)
	if err != nil {
		t.Fatal(err)
	}
	if cs.Project != ps.Project || cs.Repo != ps.Repo || cs.Repo != "example/test-repo" {
		t.Fatalf("child pin %+v parent %+v", cs, ps)
	}
	snap, err := store.LoadSnapshot(h.st, cs.Repo, cs.Item, 1)
	if err != nil || snap.MainSHA == "ccc" {
		t.Fatalf("child snapshot %+v %v", snap, err)
	}
}

func TestChildTreeLeaseCapBlocksCrossProjectClaim(t *testing.T) {
	h := setup(t)
	raw := fixture + guestProjectYAML
	pol, err := policy.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(pol)
	h.f.Put(snapshot.Item{Repo: "example/guest-repo", Item: 1, ItemKind: "issue", State: "open", MainSHA: "bbb", DefaultBranch: "main"})
	parent, err := h.e.StartRun("test", "investigate", "")
	if err != nil {
		t.Fatal(err)
	}
	child, err := h.e.StartChild(parent, "look", "guest")
	if err != nil {
		t.Fatal(err)
	}
	c1 := h.claim()
	if c1.Job.Repo != "example/test-repo" {
		t.Fatalf("parent claim %+v", c1.Job)
	}
	c2, err := h.e.Claim("example/guest-repo")
	if err != nil || c2 != nil {
		t.Fatalf("child stayed queued under parent cap, got %+v %v", c2, err)
	}
	if _, err := h.e.Complete(c1.Job.ID, c1.Job.LeaseGeneration, c1.Job.ClaimedRevision, runArt(c1)); err != nil {
		t.Fatal(err)
	}
	c3, err := h.e.Claim("example/guest-repo")
	cs, _ := store.GetSession(h.st, child)
	if err != nil || c3 == nil || cs == nil || c3.Job.Repo != cs.Repo || c3.Job.Item != cs.Item {
		t.Fatalf("child claim after parent complete %+v %v child %+v", c3, err, cs)
	}
}

func TestChildTreeLeaseAllowsWhenParentCapHasRoom(t *testing.T) {
	h := twoProjectChild(t)
	parent, err := h.e.StartRun("test", "investigate", "")
	if err != nil {
		t.Fatal(err)
	}
	child, err := h.e.StartChild(parent, "look", "guest")
	if err != nil {
		t.Fatal(err)
	}
	c1 := h.claim()
	if c1.Job.Repo != "example/test-repo" {
		t.Fatalf("parent claim %+v", c1.Job)
	}
	cs, err := store.GetSession(h.st, child)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := h.e.Claim("example/guest-repo")
	if err != nil || c2 == nil || c2.Job.Repo != cs.Repo || c2.Job.Item != cs.Item {
		t.Fatalf("child claim under parent cap 2 %+v %v", c2, err)
	}
}

func TestChildTreeLeaseSkipsBlockedChildForUnrelatedClaim(t *testing.T) {
	h := setup(t)
	raw := fixture + guestProjectYAML
	pol, err := policy.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(pol)
	h.f.Put(snapshot.Item{Repo: "example/guest-repo", Item: 1, ItemKind: "issue", State: "open", MainSHA: "bbb", DefaultBranch: "main"})
	parent, err := h.e.StartRun("test", "investigate", "")
	if err != nil {
		t.Fatal(err)
	}
	child, err := h.e.StartChild(parent, "look", "guest")
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := h.e.StartRun("guest", "other work", "")
	if err != nil {
		t.Fatal(err)
	}
	c1 := h.claim()
	if c1.Job.Repo != "example/test-repo" {
		t.Fatalf("parent claim %+v", c1.Job)
	}
	cs, err := store.GetSession(h.st, child)
	if err != nil {
		t.Fatal(err)
	}
	us, err := store.GetSession(h.st, unrelated)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := h.e.Claim("example/guest-repo")
	if err != nil || c2 == nil || c2.Job.Item != us.Item || c2.Job.Repo != us.Repo {
		t.Fatalf("unrelated guest claim %+v %v want item %d child item %d", c2, err, us.Item, cs.Item)
	}
}

func TestArchiveParentStopsLeasedChild(t *testing.T) {
	h := setup(t)
	parent, err := h.e.StartRun("test", "investigate", "")
	if err != nil {
		t.Fatal(err)
	}
	child, err := h.e.StartChild(parent, "look", "")
	if err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	c = h.claim()
	if err := h.e.ArchiveSession(parent); err != nil {
		t.Fatal(err)
	}
	turns, err := store.ListTurnsForSession(h.st, child)
	if err != nil || len(turns) != 1 {
		t.Fatalf("turns %v: %v", turns, err)
	}
	if turns[0].State != "failed" {
		t.Fatalf("child still %s", turns[0].State)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err == nil {
		t.Fatal("cancelled child completed")
	}
	acts, err := store.ListActionsForSession(h.st, parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range acts {
		if a.Type == "child.result" {
			t.Fatalf("archived parent received %s", a.Body)
		}
	}
}

func TestChildCompletionDoesNotReportToArchivedParent(t *testing.T) {
	h := setup(t)
	parent, err := h.e.StartRun("test", "investigate", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.e.StartChild(parent, "look", "")
	if err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	c = h.claim()
	// Model a parent archived by an earlier server that did not stop children.
	if err := store.SetSessionArchived(h.st, parent, true, h.clk.T); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	acts, err := store.ListActionsForSession(h.st, parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range acts {
		if a.Type == "child.result" {
			t.Fatalf("archived parent received %s", a.Body)
		}
	}
}

func TestChildFanOutCapacityReturnsAfterTerminalTurns(t *testing.T) {
	for _, outcome := range []string{"complete", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			h := setup(t)
			pol, err := policy.Parse([]byte(strings.Replace(fixture, "test:\n", "test:\n    budgets: {max_concurrent_leases: 2}\n", 1)))
			if err != nil {
				t.Fatal(err)
			}
			h.e.ReloadPolicy(pol)
			parent, err := h.e.StartRun("test", "investigate", "")
			if err != nil {
				t.Fatal(err)
			}
			p := h.claim()
			if _, err := h.e.Complete(p.Job.ID, p.Job.LeaseGeneration, p.Job.ClaimedRevision, runArt(p)); err != nil {
				t.Fatal(err)
			}
			children := make([]int64, 2)
			for i := range children {
				children[i], err = h.e.StartChild(parent, "look", "")
				if err != nil {
					t.Fatal(err)
				}
			}
			first, second := h.claim(), h.claim()
			if _, err := h.e.StartChild(parent, "too many", ""); err == nil || err.Error() != "fan-out" {
				t.Fatalf("leased fan-out: %v", err)
			}
			if outcome == "complete" {
				for _, c := range []*engine.Claim{first, second} {
					if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				for _, child := range children {
					if err := h.e.CancelSession(child); err != nil {
						t.Fatal(err)
					}
				}
			}
			replacements := make([]int64, len(children))
			for i := range replacements {
				replacements[i], err = h.e.StartChild(parent, "next batch", "")
				if err != nil {
					t.Fatalf("capacity did not return after %s: %v", outcome, err)
				}
			}
			if _, err := h.e.StartChild(parent, "queued overflow", ""); err == nil || err.Error() != "fan-out" {
				t.Fatalf("queued fan-out: %v", err)
			}
			for _, delivery := range []string{"follow-up", "queued", "steer"} {
				var err error
				switch delivery {
				case "follow-up":
					_, _, err = h.e.PromptFollowUp(children[0], "resume")
				case "queued":
					_, _, _, err = h.e.PromptQueued(children[0], "resume")
				case "steer":
					_, _, _, err = h.e.PromptSteer(children[0], "resume")
				}
				if err == nil || err.Error() != "fan-out" {
					t.Errorf("%s reactivated child beyond capacity: %v", delivery, err)
				}
			}
			if err := h.e.CancelSession(replacements[0]); err != nil {
				t.Fatal(err)
			}
			if _, _, err := h.e.PromptFollowUp(children[0], "resume with capacity"); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := h.e.PromptSteer(children[0], "same active child"); err != nil {
				t.Fatal(err)
			}
			if _, _, err := h.e.PromptFollowUp(children[1], "overflow again"); err == nil || err.Error() != "fan-out" {
				t.Fatalf("reactivation fan-out: %v", err)
			}

		})
	}
}

type childPinGitHub struct {
	gh.Client
	sha string
	err error
}

func (g childPinGitHub) DefaultSHA(string) (string, error) { return g.sha, g.err }

func TestChildSameProjectRequiresRepositoryPin(t *testing.T) {
	unavailable := errors.New("default branch unavailable")
	for _, tc := range []struct {
		name, sha   string
		err         error
		unsupported bool
	}{
		{name: "lookup error", err: unavailable},
		{name: "empty pin"},
		{name: "lookup unsupported", unsupported: true},
		{name: "pinned", sha: "child-main-sha"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := setup(t)
			parent, err := h.e.StartRun("test", "parent", "")
			if err != nil {
				t.Fatal(err)
			}
			if tc.unsupported {
				h.e.GitHub = struct{ gh.Client }{h.f}
			} else {
				h.e.GitHub = childPinGitHub{Client: h.f, sha: tc.sha, err: tc.err}
			}
			child, err := h.e.StartChild(parent, "child", "")
			if tc.sha == "" {
				if err == nil || !strings.Contains(err.Error(), "pin") {
					t.Fatalf("missing pin admitted child %d: %v", child, err)
				}
				if tc.err != nil && !errors.Is(err, tc.err) {
					t.Fatalf("lost cause: %v", err)
				}
				children, err := store.ListChildSessionIDs(h.st, parent)
				if err != nil || len(children) != 0 {
					t.Fatalf("refused child persisted: %v %v", children, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			cs, err := store.GetSession(h.st, child)
			if err != nil {
				t.Fatal(err)
			}
			snap, err := store.LoadSnapshot(h.st, cs.Repo, cs.Item, 1)
			if err != nil || snap.MainSHA != tc.sha {
				t.Fatalf("pin %+v: %v", snap, err)
			}
		})
	}
}

func TestChildWithoutRepositoryKeepsEmptyPin(t *testing.T) {
	h := setup(t)
	pol, err := policy.Parse([]byte("version: 2\nprojects:\n  scratch:\n    session_kinds: [run]\n"))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(pol)
	parent, err := h.e.StartRun("scratch", "parent", "")
	if err != nil {
		t.Fatal(err)
	}
	h.e.GitHub = childPinGitHub{Client: h.f, err: errors.New("not a repository")}
	child, err := h.e.StartChild(parent, "child", "")
	if err != nil {
		t.Fatal(err)
	}
	cs, err := store.GetSession(h.st, child)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := store.LoadSnapshot(h.st, cs.Repo, cs.Item, 1)
	if err != nil || snap.MainSHA != "" || cs.Repo != policy.ProjectKey("scratch") {
		t.Fatalf("project-only pin %+v %+v: %v", cs, snap, err)
	}
}

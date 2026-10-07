package engine_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

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
	child, err := h.e.StartChild(parent, "look at tests")
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
	if _, err := h.e.StartChild(child, "deeper"); err == nil || err.Error() != "depth" {
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

func TestChildCancelCascadesAndFanOut(t *testing.T) {
	h := setup(t)
	parent, err := h.e.StartRun("test", "investigate", "")
	if err != nil {
		t.Fatal(err)
	}
	child, err := h.e.StartChild(parent, "look")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.StartChild(parent, "again"); err == nil || err.Error() != "fan-out" {
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

package engine_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/store"
)

func pin() engine.TaskSpec {
	return engine.TaskSpec{
		EffortKey:    "effort-1",
		Prompt:       "implement the pin",
		Repo:         "example/test-repo",
		Ref:          "main",
		BaseSHA:      "aaa",
		AllowedPaths: []string{"internal/engine"},
		ContextRefs:  []string{"docs/decisions/0013-publication-authority.md"},
	}
}

func TestStartTaskRecordsPinAndReusesSpec(t *testing.T) {
	h := setup(t)
	a, err := h.e.StartTask("test", pin())
	if err != nil || a == nil || a.SessionID == 0 || a.Revision != 1 {
		t.Fatalf("%+v %v", a, err)
	}
	b, err := h.e.StartTask("test", pin())
	if err != nil || b.ID != a.ID || b.SessionID != a.SessionID {
		t.Fatalf("reuse %+v %+v %v", a, b, err)
	}
	got, err := store.GetTask(h.st, a.ID)
	if err != nil || got.BaseSHA != "aaa" || got.Repo != "example/test-repo" || got.PolicyHash == "" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestStartTaskNewRevisionOnPinChange(t *testing.T) {
	h := setup(t)
	a, err := h.e.StartTask("test", pin())
	if err != nil {
		t.Fatal(err)
	}
	spec := pin()
	spec.BaseSHA = "bbb"
	h.f.Put(issue(1)) // DefaultSHA still aaa from empty items... Fake returns aaa unless items have MainSHA
	// Override by putting an item with MainSHA bbb so stale check passes.
	it := issue(1)
	it.MainSHA = "bbb"
	h.f.Put(it)
	b, err := h.e.StartTask("test", spec)
	if err != nil {
		t.Fatal(err)
	}
	if b.Revision != 2 || b.SessionID == a.SessionID {
		t.Fatalf("revision %+v vs %+v", b, a)
	}
	old, err := store.GetTask(h.st, a.ID)
	if err != nil || old.State != "superseded" {
		t.Fatalf("old %+v %v", old, err)
	}
}

func TestStartTaskFailClosed(t *testing.T) {
	h := setup(t)
	missing := pin()
	missing.BaseSHA = ""
	if _, err := h.e.StartTask("test", missing); err == nil {
		t.Fatal("missing pin")
	}
	stale := pin()
	stale.BaseSHA = "nope"
	if _, err := h.e.StartTask("test", stale); !errors.Is(err, engine.ErrStaleSource) {
		t.Fatalf("stale %v", err)
	}
	scope := pin()
	scope.InstructionPaths = []string{"cmd/rusui"}
	if _, err := h.e.StartTask("test", scope); !errors.Is(err, engine.ErrOutOfScope) {
		t.Fatalf("scope %v", err)
	}
	unbound := pin()
	unbound.Repo = "other/repo"
	if _, err := h.e.StartTask("test", unbound); err == nil {
		t.Fatal("unbound")
	}
}

func TestCreatePinnedTaskHTTP(t *testing.T) {
	h := setup(t)
	body, _ := json.Marshal(map[string]any{
		"kind": "run", "prompt": "implement the pin", "effort_key": "effort-http",
		"repo": "example/test-repo", "ref": "main", "base_sha": "aaa",
		"allowed_paths": []string{"internal/engine"},
	})
	req, err := http.NewRequest("POST", h.http.URL+"/projects/test/sessions", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("code %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

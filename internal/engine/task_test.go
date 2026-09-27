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

func TestStartTaskReusesEffortWhenPromptChanges(t *testing.T) {
	h := setup(t)
	a, err := h.e.StartTask("test", pin())
	if err != nil {
		t.Fatal(err)
	}
	spec := pin()
	spec.Prompt = "a different prompt"
	b, err := h.e.StartTask("test", spec)
	if err != nil {
		t.Fatal(err)
	}
	if b.ID != a.ID || b.SessionID != a.SessionID || b.Revision != 1 {
		t.Fatalf("prompt change minted %+v, want %+v", b, a)
	}
	var sessions int
	if err := h.st.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE kind=?`, store.SessionKindRun).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 {
		t.Fatalf("run sessions %d", sessions)
	}
	got, err := store.GetTask(h.st, a.ID)
	if err != nil || got.Revision != 1 || got.State != "open" {
		t.Fatalf("task %+v %v", got, err)
	}
}

func TestAbandonEffortAllowsNewRevision(t *testing.T) {
	h := setup(t)
	a, err := h.e.StartTask("test", pin())
	if err != nil {
		t.Fatal(err)
	}
	if err := h.e.AbandonEffort(a.EffortKey); err != nil {
		t.Fatal(err)
	}
	b, err := h.e.StartTask("test", pin())
	if err != nil {
		t.Fatal(err)
	}
	if b.Revision != 2 || b.SessionID == a.SessionID || b.State != "open" {
		t.Fatalf("after abandon %+v", b)
	}
	var sessions int
	if err := h.st.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE kind=?`, store.SessionKindRun).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 2 {
		t.Fatalf("run sessions %d", sessions)
	}
}

func TestStartTaskKeepsTheSessionWhenThePinChanges(t *testing.T) {
	h := setup(t)
	a, err := h.e.StartTask("test", pin())
	if err != nil {
		t.Fatal(err)
	}
	spec := pin()
	spec.BaseSHA = "bbb"
	it := issue(1)
	it.MainSHA = "bbb"
	h.f.Put(it)
	b, err := h.e.StartTask("test", spec)
	if err != nil {
		t.Fatal(err)
	}
	if b.ID != a.ID || b.SessionID != a.SessionID || b.Revision != 1 {
		t.Fatalf("pin change minted %+v, want %+v", b, a)
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

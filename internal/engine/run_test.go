package engine_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/store"
)

func TestStartRunAndClaim(t *testing.T) {
	h := setup(t)
	id, err := h.e.StartRun("test", "do the thing", "idem-1")
	if err != nil || id == 0 {
		t.Fatalf("%d %v", id, err)
	}
	id2, err := h.e.StartRun("test", "ignored", "idem-1")
	if err != nil || id2 != id {
		t.Fatalf("idem %d %d %v", id, id2, err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatalf("claim %v %v", c, err)
	}
	if c.Job.Lane != "run" {
		t.Fatalf("lane %s", c.Job.Lane)
	}
	if c.Snapshot.Body != "do the thing" {
		t.Fatalf("prompt %q", c.Snapshot.Body)
	}
	turn, err := store.GetTurn(h.st, c.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, turn.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Kind != store.SessionKindRun || sess.Prompt != "do the thing" || sess.Project != "test" {
		t.Fatalf("%+v", sess)
	}
}

func TestStartRunRejectedWithoutKind(t *testing.T) {
	h := setup(t)
	if _, err := h.e.StartRun("missing", "x", ""); err == nil {
		t.Fatal("expected policy error")
	}
}

func TestCreateRunSessionHTTP(t *testing.T) {
	h := setup(t)
	req, err := http.NewRequest("POST", h.http.URL+"/projects/test/sessions", strings.NewReader(`{"kind":"run","prompt":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	req.Header.Set("Idempotency-Key", "k1")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("code %d", res.StatusCode)
	}
	_ = res.Body.Close()
	req, _ = http.NewRequest("POST", h.http.URL+"/projects/test/sessions", strings.NewReader(`{"kind":"run","prompt":"hi"}`))
	req.Header.Set("Authorization", "Bearer wsec")
	req.Header.Set("Idempotency-Key", "k1")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("code %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

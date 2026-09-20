package engine_test

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/store"
)

func TestPromptFollowUpQueued(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	_, pending, err := h.e.PromptFollowUp(sid, "second")
	if err != nil || pending != 2 {
		t.Fatalf("pending %d %v", pending, err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	if c.Snapshot.Body != "second" {
		t.Fatalf("body %q", c.Snapshot.Body)
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil || sess.Prompt != "second" {
		t.Fatalf("%v %v", sess, err)
	}
}

func TestPromptFollowUpFIFO(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	_, p1, err := h.e.PromptFollowUp(sid, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	_, p2, err := h.e.PromptFollowUp(sid, "beta")
	if err != nil || p2 != p1 {
		t.Fatalf("second follow-up must not overwrite pending: p1=%d p2=%d %v", p1, p2, err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	if c.Snapshot.Body != "alpha" {
		t.Fatalf("first claim body %q", c.Snapshot.Body)
	}
	a := art(c, "keep", "", "")
	a.GuestSessionID = "sess-fake"
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil || sess.GuestSessionID != "sess-fake" {
		t.Fatalf("guest id %+v %v", sess, err)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil {
		t.Fatal(err)
	}
	if c2.Snapshot.Body != "beta" {
		t.Fatalf("second claim body %q", c2.Snapshot.Body)
	}
}

func TestPromptFollowUpLeasedKeepsLease(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	_, pending, err := h.e.PromptFollowUp(sid, "steer")
	if err != nil || pending <= c.Job.ClaimedRevision {
		t.Fatalf("pending %d claimed %d %v", pending, c.Job.ClaimedRevision, err)
	}
	var state string
	if err := h.st.DB.QueryRow(`SELECT state FROM jobs WHERE id=?`, c.Job.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "leased" {
		t.Fatalf("state %s", state)
	}
}

func TestFollowUpHTTP(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", h.http.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/turns", strings.NewReader(`{"prompt":"next"}`))
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), "pending_revision") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	req, _ = http.NewRequest("POST", h.http.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/turns", strings.NewReader(`{"prompt":"x"}`))
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("unauth %d", res.StatusCode)
	}
}

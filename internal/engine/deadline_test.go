package engine_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/store"
)

func TestRunDeadlineFailsWithoutClaim(t *testing.T) {
	h := setup(t)
	if _, err := h.e.StartRun("test", "implement the package", ""); err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	if c.Job.Lane != "run" || c.Job.ExecutionDeadlineAt == nil {
		t.Fatalf("run claim %+v", c.Job)
	}
	id := c.Job.ID
	h.clk.Advance(engine.RunExecDeadline)
	if err := h.e.ExpireOverdueLeases(); err != nil {
		t.Fatal(err)
	}
	turn, err := store.GetTurn(h.st, id)
	if err != nil {
		t.Fatal(err)
	}
	if turn.State != "failed" {
		t.Fatalf("state %s", turn.State)
	}
	next, err := h.e.Claim("example/test-repo")
	if err != nil {
		t.Fatal(err)
	}
	if next != nil {
		t.Fatalf("claimed #%d after deadline fail", next.Job.Item)
	}
}

func TestFailHTTPAfterRunDeadline(t *testing.T) {
	h := setup(t)
	if _, err := h.e.StartRun("test", "implement the package", ""); err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	if c.Job.Lane != "run" {
		t.Fatalf("lane %s", c.Job.Lane)
	}
	h.clk.Advance(engine.RunExecDeadline)
	if c.Job.LeaseExpiresAt == nil || h.clk.T.Before(*c.Job.LeaseExpiresAt) {
		t.Fatalf("lease still live at %s", h.clk.T)
	}
	if c.Job.ExecutionDeadlineAt == nil || h.clk.T.Before(*c.Job.ExecutionDeadlineAt) {
		t.Fatalf("deadline still live at %s", h.clk.T)
	}
	body, err := json.Marshal(map[string]int{
		"lease_generation": c.Job.LeaseGeneration,
		"claimed_revision": c.Job.ClaimedRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/jobs/%d/fail", h.http.URL, c.Job.ID), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d %s", res.StatusCode, raw)
	}
	var receipt map[string]any
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt["kind"] != "fail" {
		t.Fatalf("receipt %+v", receipt)
	}
	turn, err := store.GetTurn(h.st, c.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turn.State != "failed" {
		t.Fatalf("state %s", turn.State)
	}
	again, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/jobs/%d/fail", h.http.URL, c.Job.ID), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	again.Header.Set("Authorization", "Bearer wsec")
	again.Header.Set("Content-Type", "application/json")
	res2, err := http.DefaultClient.Do(again)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res2.Body.Close() }()
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("idempotent fail %d", res2.StatusCode)
	}
}

func TestExpireLeaseDoesNotRequeueDeadlineRun(t *testing.T) {
	h := setup(t)
	if _, err := h.e.StartRun("test", "implement the package", ""); err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	id := c.Job.ID
	h.clk.Advance(engine.RunExecDeadline)
	next, err := h.e.Claim("example/test-repo")
	if err != nil {
		t.Fatal(err)
	}
	if next != nil {
		t.Fatalf("requeued as #%d", next.Job.Item)
	}
	turn, err := store.GetTurn(h.st, id)
	if err != nil {
		t.Fatal(err)
	}
	if turn.State != "failed" {
		t.Fatalf("state %s", turn.State)
	}
}

func TestRunDeadlinePromotesSteer(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "implement the package", "")
	if err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	if c.Job.Lane != "run" {
		t.Fatalf("lane %s", c.Job.Lane)
	}
	if _, _, live, err := h.e.PromptSteer(sid, "keep this steer"); err != nil || !live {
		t.Fatalf("live %t err %v", live, err)
	}
	h.clk.Advance(engine.RunExecDeadline)
	if err := h.e.ExpireOverdueLeases(); err != nil {
		t.Fatal(err)
	}
	next, err := h.e.Claim("example/test-repo")
	if err != nil || next == nil || next.Snapshot.Body != "keep this steer" {
		t.Fatalf("promoted steer %+v %v", next, err)
	}
}

func TestRunDeadlineAppliesQueuedFollowUp(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "implement the package", "")
	if err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	if _, _, err := h.e.PromptFollowUp(sid, "first follow-up"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.e.PromptFollowUp(sid, "second follow-up"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.Fail(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision); err != nil {
		t.Fatal(err)
	}
	first := h.claim()
	if first.Snapshot.Body != "first follow-up" {
		t.Fatalf("first %q", first.Snapshot.Body)
	}
	h.clk.Advance(engine.RunExecDeadline)
	if err := h.e.ExpireOverdueLeases(); err != nil {
		t.Fatal(err)
	}
	second, err := h.e.Claim("example/test-repo")
	if err != nil || second == nil || second.Snapshot.Body != "second follow-up" {
		t.Fatalf("queued follow-up %+v %v", second, err)
	}
}

func TestLostHeartbeatBeforeDeadlineRetries(t *testing.T) {
	h := setup(t)
	if _, err := h.e.StartRun("test", "implement the package", ""); err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	id := c.Job.ID
	h.clk.Advance(4 * time.Minute)
	next := h.claim()
	if next.Job.ID != id {
		t.Fatalf("claimed %d want %d", next.Job.ID, id)
	}
	if next.Job.State != "leased" {
		t.Fatalf("state %s", next.Job.State)
	}
	turn, err := store.GetTurn(h.st, id)
	if err != nil {
		t.Fatal(err)
	}
	if turn.State != "leased" || turn.RetryCount < 1 {
		t.Fatalf("retry %+v", turn)
	}
}

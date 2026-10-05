package runner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/provider"
)

// approvalOnceServer accepts the first acp.approval as "A" (operator-allowed)
// and fails every later one. Other receipts are accepted.
func approvalOnceServer(t *testing.T) *HTTPRecorder {
	t.Helper()
	var approvals atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.URL.Path != "/approvals/A" {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, `{"decision":"allow","valid":true}`)
			return
		}
		var body struct {
			Type string `json:"type"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Type == acp.ActionApproval && approvals.Add(1) > 1 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		id := "r"
		if body.Type == acp.ActionApproval {
			id = "A"
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"id":"`+id+`"}`)
	}))
	t.Cleanup(server.Close)
	return &HTTPRecorder{Base: server.URL, Token: "turn-token", TurnID: 1, HTTP: server.Client()}
}

func acpPermissionOption(t *testing.T, rec *HTTPRecorder) string {
	t.Helper()
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	answered := make(chan string, 1)
	agent := &acp.FakeAgent{In: agentIn, Out: agentOut, PermissionOption: func(o string) { answered <- o }}
	go func() { _ = agent.Run() }()
	t.Cleanup(func() { _ = clientIn.Close(); _ = clientOut.Close(); _ = agentIn.Close(); _ = agentOut.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := &acp.Client{In: clientIn, Out: clientOut, Rec: rec, Perm: acp.DenyUnmatched{}, Wait: rec.Wait, Ctx: ctx}
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sid, err := c.SessionNew(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SessionPrompt(ctx, sid, "go"); err != nil {
		t.Fatal(err)
	}
	select {
	case o := <-answered:
		return o
	case <-ctx.Done():
		t.Fatal("no permission answer")
	}
	return ""
}

func TestACPPermissionFailedApprovalRecordDoesNotReusePriorApproval(t *testing.T) {
	rec := approvalOnceServer(t)
	if got := acpPermissionOption(t, rec); got != "allow-once" {
		t.Fatalf("first request answered %q, want allow-once off its own approval", got)
	}
	if got := acpPermissionOption(t, rec); got != "reject-once" {
		t.Fatalf("second request answered %q after its approval failed to record, want reject-once", got)
	}
}

func TestClaudePermissionFailedApprovalRecordDoesNotReusePriorApproval(t *testing.T) {
	rec := approvalOnceServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	host := &acp.Client{Rec: rec, Wait: rec.Wait, Ctx: ctx}
	decide := claudeDecide(&Assignment{}, host)
	options := []provider.Option{{ID: "allow"}}
	if id, ok := decide(options, json.RawMessage(`{"command":"make a"}`)); !ok || id != "allow" {
		t.Fatalf("first request = %q, %v; want allow off its own approval", id, ok)
	}
	if id, ok := decide(options, json.RawMessage(`{"command":"make b"}`)); ok {
		t.Fatalf("second request = %q, allowed after its approval failed to record", id)
	}
}

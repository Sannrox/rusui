package runner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/engine"
)

func TestHostACPLoadsOrCreatesGuestSession(t *testing.T) {
	t.Parallel()
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	go func() { _ = (&acp.FakeAgent{In: agentIn, Out: agentOut}).Run() }()
	host := &acp.Client{In: clientIn, Out: clientOut, Perm: acp.DenyUnmatched{}, Wait: func(context.Context, acp.PermissionParams) acp.Decision {
		return acp.Decision{}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	in, _ := json.Marshal(map[string]string{"title": "ping", "body": "pong"})
	art, err := HostACP(ctx, &Assignment{GuestSessionID: "missing", Input: in, Repo: "example/test-repo", Item: 1, ItemKind: "issue"}, host, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if art.GuestSessionID != "sess-fake" {
		t.Fatalf("restore-failed must session/new, got %q", art.GuestSessionID)
	}
}

func TestHostACPReusesGuestSession(t *testing.T) {
	t.Parallel()
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	go func() { _ = (&acp.FakeAgent{In: agentIn, Out: agentOut}).Run() }()
	host := &acp.Client{In: clientIn, Out: clientOut, Perm: acp.DenyUnmatched{}, Wait: func(context.Context, acp.PermissionParams) acp.Decision {
		return acp.Decision{}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	in, _ := json.Marshal(map[string]string{"body": "ping"})
	art, err := HostACP(ctx, &Assignment{GuestSessionID: "sess-fake", Input: in}, host, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if art.GuestSessionID != "sess-fake" {
		t.Fatalf("load kept %q", art.GuestSessionID)
	}
}

func TestHTTPRecorderWaitCancelsBlockedApprovalPoll(t *testing.T) {
	pollStarted := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"id":"approval-1"}`)
			return
		}
		select {
		case pollStarted <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer func() {
		close(release)
		server.Close()
	}()
	recorder := &HTTPRecorder{Base: server.URL, Token: "turn-token", TurnID: 1, HTTP: server.Client()}
	if err := recorder.Record(acp.Receipt{Type: acp.ActionApproval}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan acp.Decision, 1)
	go func() { done <- recorder.Wait(ctx, acp.PermissionParams{}) }()
	select {
	case <-pollStarted:
	case <-time.After(time.Second):
		t.Fatal("approval poll did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("approval wait did not return after cancellation")
	}
}

func TestHeartbeatSteersRenewsLeaseWhileDeliveryIsBlocked(t *testing.T) {
	var heartbeatCount atomic.Int32
	type heartbeatObservation struct {
		turnID int64
		count  int32
		acks   []int64
	}
	observations := make(chan heartbeatObservation, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			AckSteerIDs []int64 `json:"ack_steer_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		var turnID int64
		switch r.URL.Path {
		case "/jobs/1/heartbeat":
			turnID = 1
		case "/jobs/2/heartbeat":
			turnID = 2
		}
		count := heartbeatCount.Add(1)
		observations <- heartbeatObservation{turnID: turnID, count: count, acks: request.AckSteerIDs}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"steer": engine.Steer{ID: int64(count), Prompt: "prompt"},
		})
	}))
	defer server.Close()

	client := &Client{Base: server.URL, HTTP: server.Client()}
	assignment := &Assignment{TurnID: 1, LeaseGeneration: 1, ClaimedRevision: 1, TurnToken: "turn-token"}
	steers := make(chan engine.Steer)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		client.heartbeatSteers(context.Background(), assignment, steers, stop)
		close(done)
	}()
	for want := int32(1); want <= 3; want++ {
		select {
		case got := <-observations:
			if got.turnID != 1 || got.count != want {
				t.Fatalf("heartbeat observation %+v, want turn 1 count %d", got, want)
			}
			if want == 1 && len(got.acks) != 0 {
				t.Fatalf("first heartbeat acknowledged steers %v", got.acks)
			}
			if want > 1 && (len(got.acks) != 1 || got.acks[0] != int64(want-1)) {
				t.Fatalf("heartbeat %d acknowledged steers %v", want, got.acks)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d heartbeats arrived while steer delivery was blocked", want-1)
		}
	}
	close(stop)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat loop did not stop")
	}
	nextAssignment := &Assignment{TurnID: 2, LeaseGeneration: 1, ClaimedRevision: 1, TurnToken: "next-token"}
	if _, err := client.PollHeartbeat(nextAssignment); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-observations:
			if got.turnID == 2 {
				if len(got.acks) != 0 {
					t.Fatalf("next assignment inherited steer receipts %v", got.acks)
				}
				return
			}
		case <-deadline:
			t.Fatal("next assignment heartbeat was not observed")
		}
	}
}

func TestSteerClearsInterruptedRunResult(t *testing.T) {
	dir := t.TempDir()
	resultPath := filepath.Join(dir, "result.json")
	if err := os.WriteFile(resultPath, []byte(`{"blocked_reason":"stale result"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	promptStarted := make(chan acp.PromptParams, 2)
	agent := &acp.FakeAgent{
		In:            agentIn,
		Out:           agentOut,
		PromptStarted: promptStarted,
		PromptHandler: func(p acp.PromptParams, cancel <-chan struct{}, _ func(string, any) error) (acp.PromptResult, error) {
			if strings.Contains(p.Prompt[0].Text, "original prompt") {
				<-cancel
				return acp.PromptResult{StopReason: "cancelled"}, nil
			}
			return acp.PromptResult{StopReason: "end_turn"}, nil
		},
	}
	go func() { _ = agent.Run() }()
	host := &acp.Client{In: clientIn, Out: clientOut, Perm: acp.DenyUnmatched{}}
	in, _ := json.Marshal(map[string]string{"body": "original prompt"})
	a := &Assignment{Repo: "example/test-repo", Item: 1, ItemKind: "run", Input: in, ResultPath: resultPath}
	steers := make(chan engine.Steer)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		select {
		case <-promptStarted:
			steers <- engine.Steer{ID: 41, Prompt: "Use the corrected plan"}
		case <-ctx.Done():
		}
		close(done)
	}()
	art, steerIDs, err := hostACP(ctx, a, host, dir, steers, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("steer was not delivered")
	}
	select {
	case prompt := <-promptStarted:
		if !strings.Contains(prompt.Prompt[0].Text, "Use the corrected plan") || !strings.Contains(prompt.Prompt[0].Text, resultInstructions) {
			t.Fatalf("steered prompt did not retain run result instructions: %q", prompt.Prompt[0].Text)
		}
	case <-ctx.Done():
		t.Fatal("replacement prompt did not start")
	}
	if len(steerIDs) != 1 || steerIDs[0] != 41 {
		t.Fatalf("completed steer IDs %v", steerIDs)
	}
	if result := collectResult(nil, a, dir, art.SnapshotHash); result != nil {
		t.Fatalf("interrupted prompt's stale report was accepted: %+v", result)
	}
}

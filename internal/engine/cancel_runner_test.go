package engine_test

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/runner"
)

// A cancel that lands after the claim but before the harness starts kills
// nothing in the guest. The runner's heartbeat then finds the lease gone
// and tears the harness down instead of running the turn on a dead lease
// (#472). When the cancel requeues the turn and another runner claims it,
// the first harness is still torn down, so the turn never runs twice.
func TestCancelBeforeHarnessStartStopsTheHarness(t *testing.T) {
	for _, tc := range []struct {
		name    string
		requeue bool
	}{
		{name: "cancelled"},
		{name: "requeued and claimed again", requeue: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := setup(t)
			h.clk.T = time.Now().UTC()
			sid, err := h.e.StartRun("test", "bug: original prompt", "")
			if err != nil {
				t.Fatal(err)
			}
			var finished, stopped atomic.Bool
			torn := make(chan struct{})
			clientIn, agentOut := io.Pipe()
			agentIn, clientOut := io.Pipe()
			agent := &acp.FakeAgent{
				In: agentIn, Out: agentOut,
				PromptHandler: func(acp.PromptParams, <-chan struct{}, func(string, any) error) (acp.PromptResult, error) {
					select {
					case <-torn:
						return acp.PromptResult{StopReason: "cancelled"}, nil
					case <-time.After(2 * time.Second):
						finished.Store(true)
						return acp.PromptResult{StopReason: "end_turn"}, nil
					}
				},
			}
			go func() { _ = agent.Run() }()
			stop := func() {
				if stopped.CompareAndSwap(false, true) {
					close(torn)
				}
				_ = clientIn.Close()
				_ = clientOut.Close()
				_ = agentIn.Close()
				_ = agentOut.Close()
			}
			t.Cleanup(stop)
			client := &runner.Client{Base: h.http.URL, Bootstrap: "wsec", Repo: "example/test-repo"}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_, err = runner.OneACPTurnWithOutcome(ctx, client, func(a *runner.Assignment, _ string) (*acp.Client, func(), error) {
				// The cancel lands in the window between claim and harness exec.
				if tc.requeue {
					if _, _, live, err := h.e.PromptSteer(sid, "continue after cancellation"); err != nil || !live {
						t.Errorf("steer live %t err %v", live, err)
					}
				}
				if err := h.e.CancelSession(sid); err != nil {
					t.Error(err)
				}
				if tc.requeue {
					other := &runner.Client{Base: h.http.URL, Bootstrap: "wsec", Repo: "example/test-repo", Name: "other"}
					if err := other.Hello(); err != nil {
						t.Error(err)
					}
					if out, err := other.ClaimWithOutcome(); err != nil || out.Assignment == nil || out.Assignment.TurnID != a.TurnID {
						t.Errorf("requeued turn not claimed again: %+v %v", out, err)
					}
				}
				return &acp.Client{In: clientIn, Out: clientOut, Perm: acp.DenyUnmatched{}}, stop, nil
			})
			if !errors.Is(err, runner.ErrLeaseLost) {
				t.Fatalf("runner err %v, want lease lost", err)
			}
			if finished.Load() {
				t.Fatal("harness ran the turn to the end on a cancelled lease")
			}
			if !stopped.Load() {
				t.Fatal("harness was not stopped")
			}
		})
	}
}

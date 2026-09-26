package acp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"
)

func TestSessionCancelCancelsPendingPermission(t *testing.T) {
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	permissionRequested := make(chan struct{}, 1)
	permissionResult := make(chan PermissionOutcome, 1)
	cancelReceived := make(chan SessionCancelParams, 1)
	agent := &FakeAgent{
		In:                  agentIn,
		Out:                 agentOut,
		PermissionRequested: permissionRequested,
		PermissionResult:    func(out PermissionOutcome) { permissionResult <- out },
		CancelReceived:      cancelReceived,
		PromptHandler: func(p PromptParams, _ <-chan struct{}, request func(string, any) error) (PromptResult, error) {
			err := request(MethodRequestPermission, PermissionParams{
				SessionID: p.SessionID,
				ToolCall:  []byte(`{"toolCallId":"pending"}`),
				Options: []PermOption{
					{OptionID: "allow-once", Name: "Allow", Kind: "allow_once"},
					{OptionID: "reject-once", Name: "Reject", Kind: "reject_once"},
				},
			})
			if err != nil {
				return PromptResult{}, err
			}
			return PromptResult{StopReason: "cancelled"}, nil
		},
	}
	go func() { _ = agent.Run() }()
	client := &Client{
		In:   clientIn,
		Out:  clientOut,
		Perm: DenyUnmatched{},
		Wait: func(ctx context.Context, _ PermissionParams) Decision {
			<-ctx.Done()
			return Decision{}
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sessionID, err := client.SessionNew(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	promptDone := make(chan error, 1)
	go func() {
		_, err := client.SessionPrompt(ctx, sessionID, "original")
		promptDone <- err
	}()
	select {
	case <-permissionRequested:
	case <-ctx.Done():
		t.Fatal("permission request did not reach the host")
	}
	if err := client.SessionCancel(sessionID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-promptDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("cancelled prompt did not finish")
	}
	select {
	case outcome := <-permissionResult:
		if outcome.Outcome.Outcome != "cancelled" || outcome.Outcome.OptionID != "" {
			t.Fatalf("pending permission outcome %+v", outcome)
		}
	case <-ctx.Done():
		t.Fatal("permission response was not recorded")
	}
	select {
	case got := <-cancelReceived:
		if got.SessionID != sessionID {
			t.Fatalf("cancel session %q, want %q", got.SessionID, sessionID)
		}
	case <-ctx.Done():
		t.Fatal("session/cancel was not delivered")
	}
}

type blockedPromptWriter struct {
	writer  io.Writer
	entered chan<- struct{}
	release <-chan struct{}
}

func (w blockedPromptWriter) Write(p []byte) (int, error) {
	var msg rpcMessage
	if err := json.Unmarshal(p, &msg); err == nil && msg.Method == MethodSessionPrompt {
		w.entered <- struct{}{}
		<-w.release
	}
	return w.writer.Write(p)
}

func TestSessionPromptSubmittedWaitsForRequestWrite(t *testing.T) {
	clientIn, agentOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = agentOut.Close()
	})
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	submitted := make(chan struct{})
	client := &Client{
		In: clientIn,
		Out: blockedPromptWriter{
			writer:  io.Discard,
			entered: entered,
			release: release,
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.SessionPromptSubmitted(ctx, "session-1", "prompt", submitted)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("session/prompt write did not start")
	}
	select {
	case <-submitted:
		t.Fatal("prompt was reported submitted before its write completed")
	default:
	}
	close(release)
	select {
	case <-submitted:
	case <-time.After(5 * time.Second):
		t.Fatal("prompt write completed without submission signal")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("prompt error %v, want canceled context", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("prompt call did not stop after context cancellation")
	}
}

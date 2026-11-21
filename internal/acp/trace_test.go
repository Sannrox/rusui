package acp

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestSessionPromptCarriesTraceMeta(t *testing.T) {
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	started := make(chan PromptParams, 1)
	go func() {
		_ = (&FakeAgent{In: agentIn, Out: agentOut, PromptStarted: started}).Run()
	}()
	c := &Client{In: clientIn, Out: clientOut, Perm: DenyUnmatched{}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sid, err := c.SessionNew(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const header = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	meta := map[string]any{"traceparent": header, "tracestate": "vendor=one"}
	if _, err := c.SessionPromptSubmittedBlocks(ctx, sid, []PromptBlock{{Type: "text", Text: "ping"}}, nil, meta); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-started:
		if p.Meta["traceparent"] != header || p.Meta["tracestate"] != "vendor=one" {
			t.Fatalf("meta %+v", p.Meta)
		}
	case <-ctx.Done():
		t.Fatal("prompt")
	}
}

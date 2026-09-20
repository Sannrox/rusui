package acp

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

func TestUnmatchedPermissionWaitsThenAllowOnce(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "wait.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	go func() { _ = (&FakeAgent{In: agentIn, Out: agentOut}).Run() }()
	waited := make(chan struct{}, 1)
	c := &Client{
		In:   clientIn,
		Out:  clientOut,
		Rec:  StoreRecorder{Store: st, Repo: "example/test-repo", Item: 1},
		Perm: DenyUnmatched{},
		Wait: func(ctx context.Context, p PermissionParams) Decision {
			waited <- struct{}{}
			return Decision{Matched: true, Allow: true}
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sid, err := c.SessionNew(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SessionPrompt(ctx, sid, "ping"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-waited:
	default:
		t.Fatal("unmatched permission did not wait")
	}
}

func TestUnmatchedPermissionWaitTimeoutRejects(t *testing.T) {
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	go func() { _ = (&FakeAgent{In: agentIn, Out: agentOut}).Run() }()
	c := &Client{
		In:   clientIn,
		Out:  clientOut,
		Perm: DenyUnmatched{},
		Wait: func(ctx context.Context, p PermissionParams) Decision {
			<-ctx.Done()
			return Decision{Matched: false, Allow: false}
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	c.Ctx = ctx
	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	sid, err := c.SessionNew(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.SessionPrompt(context.Background(), sid, "ping")
	if err != nil {
		t.Fatal(err)
	}
}

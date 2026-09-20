package runner

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/acp"
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

package acp

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

func TestConformanceFakeAgent(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "acp.db"))
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

	done := make(chan error, 1)
	go func() {
		done <- (&FakeAgent{In: agentIn, Out: agentOut}).Run()
	}()

	c := &Client{
		In:   clientIn,
		Out:  clientOut,
		Rec:  StoreRecorder{Store: st, Repo: "Sannrox/rusui", Item: 10},
		Perm: DenyUnmatched{},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	init, err := c.Initialize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if init.AgentInfo["name"] != "fake-acp" {
		t.Fatalf("agent %v", init.AgentInfo)
	}
	sid, err := c.SessionNew(ctx, t.TempDir())
	if err != nil || sid != "sess-fake" {
		t.Fatalf("session/new %q %v", sid, err)
	}
	if err := c.SessionLoad(ctx, sid, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	pr, err := c.SessionPrompt(ctx, sid, "ping")
	if err != nil {
		t.Fatal(err)
	}
	if pr.StopReason != "end_turn" {
		t.Fatalf("stop %q", pr.StopReason)
	}

	acts, err := store.ListActions(st, "Sannrox/rusui", 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	var approval bool
	for _, a := range acts {
		got[a.Type]++
		if a.Type == ActionApproval && a.ReasonCode == ReasonUnmatched {
			approval = true
		}
	}
	need := []string{
		ActionFSRead, ActionFSWrite,
		ActionTerminalCreate, ActionTerminalOutput, ActionTerminalWait, ActionTerminalKill, ActionTerminalRelease,
		ActionPermission, ActionApproval, ActionUnknown, ActionUpdate,
	}
	for _, typ := range need {
		if got[typ] == 0 {
			t.Fatalf("missing receipt %s in %#v", typ, got)
		}
	}
	if !approval {
		t.Fatalf("unmatched permission was not surfaced as approval: %#v", acts)
	}
	if got[ActionUnknown] == 0 {
		t.Fatal("evil/bypass bypassed the client")
	}

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, io.EOF) {
			t.Log(err)
		}
	default:
	}
}

func TestPermissionMatchedAllowHasNoApproval(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "acp.db"))
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

	c := &Client{
		In:   clientIn,
		Out:  clientOut,
		Rec:  StoreRecorder{Store: st, Repo: "Sannrox/rusui", Item: 10},
		Perm: allowAll{},
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
	acts, err := store.ListActions(st, "Sannrox/rusui", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range acts {
		if a.Type == ActionApproval {
			t.Fatalf("matched allow should not create approval: %+v", a)
		}
	}
}

type allowAll struct{}

func (allowAll) Decide(PermissionParams) Decision {
	return Decision{Matched: true, Allow: true}
}

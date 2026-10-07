package acp

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

func TestSessionNewModeIsForwardedAndOmitted(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mode.db"))
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

	var got []string
	go func() {
		_ = (&FakeAgent{In: agentIn, Out: agentOut, SessionMode: func(mode string) {
			got = append(got, mode)
		}}).Run()
	}()

	c := &Client{In: clientIn, Out: clientOut, Rec: StoreRecorder{Store: st, Repo: "Sannrox/rusui", Item: 10}, Perm: DenyUnmatched{}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	if _, err := c.SessionNewMode(ctx, cwd, "high"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SessionNew(ctx, cwd); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "high" || got[1] != "" {
		t.Fatalf("modes %v", got)
	}
}

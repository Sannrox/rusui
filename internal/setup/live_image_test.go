package setup

import (
	"context"
	"os"
	"testing"
	"time"

	guestimage "github.com/sannrox/rusui/build/guest-image"
	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/env"
)

// TestLiveReferenceGuestImage builds the reference image with the real
// container CLI, starts it on the trusted network, and speaks ACP to the
// Claude Code adapter inside it. Needs Docker or Podman.
func TestLiveReferenceGuestImage(t *testing.T) {
	if os.Getenv("RUSUI_LIVE_DOCKER") == "" {
		t.Skip("set RUSUI_LIVE_DOCKER=1 to build and run the reference guest image")
	}
	rt, err := env.LookRuntime()
	if err != nil {
		t.Skip(err.Error())
	}
	d := rt.(env.DockerCLI)
	tag := guestimage.Tag()
	if !d.ImageExists(tag) {
		if err := d.BuildImage(tag, guestimage.Dockerfile); err != nil {
			t.Fatal(err)
		}
	}
	id, err := d.CreateAndStart(env.Spec{Name: "live-image-" + time.Now().Format("150405"), Image: tag})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Remove(id) })
	for _, tool := range [][]string{{"gh", "--version"}, {"git", "--version"}, {"node", "--version"}} {
		if err := d.Exec(id, tool); err != nil {
			t.Fatalf("%v: %v", tool, err)
		}
	}
	in, out, stop, err := d.ExecStdio(id, []string{acp.ClaudeStdio}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	c := &acp.Client{In: out, Out: in, Perm: acp.DenyUnmatched{}}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := c.Initialize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("initialize agent=%v protocol=%d", res.AgentInfo, res.ProtocolVersion)
	if res.ProtocolVersion != 1 {
		t.Fatalf("protocol %d", res.ProtocolVersion)
	}
}

package main

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/guest"
)

func TestQualifyInImageRunsProbeAndACPInGuestImage(t *testing.T) {
	entry := guest.Entry{Protocol: guest.ProtocolACP, Argv: []string{"second-agent", "acp"}, Probe: []string{"second-agent", "--version"}, Pin: "2", Image: "example/second@sha256:" + strings.Repeat("a", 64)}
	rt := &env.FakeRuntime{StdioHook: func(string, []string, []string) (io.WriteCloser, io.ReadCloser, func(), error) {
		clientIn, agentOut := io.Pipe()
		agentIn, clientOut := io.Pipe()
		go func() { _ = (&acp.FakeAgent{In: agentIn, Out: agentOut}).Run() }()
		return clientOut, clientIn, func() { _ = clientOut.Close(); _ = agentOut.Close() }, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	digest, err := qualifyInImage(ctx, rt, entry)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := guest.ConformanceDigest(entry); digest != want {
		t.Fatalf("digest %q, want %q", digest, want)
	}
	if len(rt.Created) != 1 || rt.Created[0].Image != entry.Image {
		t.Fatalf("created %+v", rt.Created)
	}
	if len(rt.Execs) != 1 || !slices.Equal(rt.Execs[0][1:], entry.Probe) {
		t.Fatalf("probe execs %v", rt.Execs)
	}
	if len(rt.Stdio) != 1 || !slices.Equal(rt.Stdio[0].Argv, entry.Argv) {
		t.Fatalf("guest execs %+v", rt.Stdio)
	}
	if len(rt.Removed) != 1 {
		t.Fatalf("container not removed: %v", rt.Removed)
	}
}

func TestQualifyInImageRefusesTagBeforeStartingContainer(t *testing.T) {
	entry := guest.Entry{Protocol: guest.ProtocolACP, Argv: []string{"second-agent"}, Probe: []string{"second-agent", "--version"}, Pin: "2", Image: "example/second:2"}
	rt := &env.FakeRuntime{}
	if _, err := qualifyInImage(context.Background(), rt, entry); err == nil {
		t.Fatal("tag-pinned image qualified")
	}
	if len(rt.Created) != 0 {
		t.Fatalf("created %+v", rt.Created)
	}
}

func TestQualifyInImageFailsWhenProbeFailsInImage(t *testing.T) {
	entry := guest.Entry{Protocol: guest.ProtocolACP, Argv: []string{"second-agent"}, Probe: []string{"second-agent", "--version"}, Pin: "2", Image: "sha256:" + strings.Repeat("a", 64)}
	rt := &env.FakeRuntime{ExecHook: func(string, []string) error { return io.ErrUnexpectedEOF }}
	if _, err := qualifyInImage(context.Background(), rt, entry); err == nil || !strings.Contains(err.Error(), "probe") {
		t.Fatalf("missing guest binary qualified: %v", err)
	}
	if len(rt.Stdio) != 0 || len(rt.Removed) != 1 {
		t.Fatalf("stdio %+v removed %v", rt.Stdio, rt.Removed)
	}
}

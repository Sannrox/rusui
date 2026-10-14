package engine_test

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
	"github.com/sannrox/rusui/internal/policy"
	"gopkg.in/yaml.v3"
)

var conformanceKey = []byte(strings.Repeat("k", 32))

func genericACP() guest.Entry {
	return guest.Entry{Protocol: guest.ProtocolACP, Argv: []string{"second-agent", "acp"}, Probe: []string{"second-agent", "--version"}, Pin: "2", Image: "example/second@sha256:" + strings.Repeat("a", 64)}
}

// fakeACPRuntime answers every stdio exec with the in-tree fake ACP agent.
func fakeACPRuntime() *env.FakeRuntime {
	return &env.FakeRuntime{StdioHook: func(string, []string, []string) (io.WriteCloser, io.ReadCloser, func(), error) {
		clientIn, agentOut := io.Pipe()
		agentIn, clientOut := io.Pipe()
		go func() { _ = (&acp.FakeAgent{In: agentIn, Out: agentOut}).Run() }()
		return clientOut, clientIn, func() { _ = clientOut.Close(); _ = agentOut.Close() }, nil
	}}
}

func TestQualifyGuestRunsSuiteInImageAndSignsReceipt(t *testing.T) {
	h := setup(t)
	rt := fakeACPRuntime()
	h.e.Container = env.Container{RT: rt, Image: "guest:operator"}
	h.e.ConformanceKey = conformanceKey
	entry := genericACP()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	receipt, err := h.e.QualifyGuest(ctx, entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.Verify(conformanceKey, entry); err != nil {
		t.Fatalf("receipt does not verify: %v", err)
	}
	if len(rt.Created) != 1 || rt.Created[0].Image != entry.Image {
		t.Fatalf("created %+v", rt.Created)
	}
	if len(rt.Execs) != 1 || !slices.Equal(rt.Execs[0][1:], entry.Probe) {
		t.Fatalf("probe execs %v", rt.Execs)
	}
	if len(rt.Stdio) != 1 || !slices.Equal(rt.Stdio[0].Argv, entry.Argv) || len(rt.Removed) != 1 {
		t.Fatalf("guest execs %+v removed %v", rt.Stdio, rt.Removed)
	}
}

func TestQualifyGuestSignsNothingWhenProbeFailsInImage(t *testing.T) {
	h := setup(t)
	rt := &env.FakeRuntime{ExecHook: func(string, []string) error { return io.ErrUnexpectedEOF }}
	h.e.Container = env.Container{RT: rt}
	h.e.ConformanceKey = conformanceKey
	if _, err := h.e.QualifyGuest(context.Background(), genericACP()); err == nil || !strings.Contains(err.Error(), "probe") {
		t.Fatalf("missing guest binary qualified: %v", err)
	}
	if len(rt.Stdio) != 0 || len(rt.Removed) != 1 {
		t.Fatalf("stdio %+v removed %v", rt.Stdio, rt.Removed)
	}
	tagged := genericACP()
	tagged.Image = "example/second:2"
	if _, err := h.e.QualifyGuest(context.Background(), tagged); err == nil || len(rt.Created) != 1 {
		t.Fatalf("tag-pinned image qualified: %v created %+v", err, rt.Created)
	}
}

func TestGenericGuestAdmissionVerifiesReceipt(t *testing.T) {
	h := setup(t)
	h.e.Container = env.Container{RT: &env.FakeRuntime{}, Image: "guest:operator"}
	h.e.ConformanceKey = conformanceKey
	var config policy.File
	if err := yaml.Unmarshal(h.e.PolicySnapshot().Raw, &config); err != nil {
		t.Fatal(err)
	}
	config.Guests = guest.Builtin()
	p := config.Projects["test"]
	p.Guests = &policy.ProjectGuests{Default: "shikigami", Allowed: []string{"shikigami", "second"}}
	config.Projects["test"] = p
	withReceipt := func(key []byte) []byte {
		t.Helper()
		entry := genericACP()
		receipt, err := guest.SignReceipt(key, entry, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		entry.Receipt = &receipt
		config.Guests["second"] = entry
		raw, err := yaml.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if err := h.e.ReloadPolicyBytes(withReceipt([]byte(strings.Repeat("x", 32)))); err == nil {
		t.Fatal("reload accepted a receipt signed without the plane key")
	}
	if err := h.e.ReloadPolicyBytes(withReceipt(conformanceKey)); err != nil {
		t.Fatal(err)
	}
	sid, err := h.e.StartRunGuest("test", "qualified", "", "", "", "second")
	if err != nil {
		t.Fatalf("qualified guest refused: %v", err)
	}
	// A new key requalifies the registry entry, but the session's frozen
	// entry still carries the old key's receipt and must not spawn.
	h.e.ConformanceKey = []byte(strings.Repeat("n", 32))
	if err := h.e.ReloadPolicyBytes(withReceipt(h.e.ConformanceKey)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.e.SessionGuest(sid); err == nil {
		t.Fatal("session pinned under the old key resumed")
	}
	if _, err := h.e.StartRunGuest("test", "requalified", "", "", "", "second"); err != nil {
		t.Fatalf("requalified guest refused: %v", err)
	}
	h.e.ConformanceKey = nil
	if _, err := h.e.StartRunGuest("test", "keyless", "", "", "", "second"); err == nil {
		t.Fatal("plane without a conformance key admitted a generic guest")
	}
}

func TestQualifyGuestDeadlineEndsHungProbe(t *testing.T) {
	h := setup(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	rt := &env.FakeRuntime{ExecHook: func(string, []string) error { <-release; return nil }}
	h.e.Container = env.Container{RT: rt}
	h.e.ConformanceKey = conformanceKey
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := h.e.QualifyGuest(ctx, genericACP()); err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("hung probe qualified: %v", err)
	}
	if len(rt.Removed) != 1 {
		t.Fatalf("container left running: %v", rt.Removed)
	}
}

func TestQualifyGuestDeadlineEndsHungCreate(t *testing.T) {
	h := setup(t)
	release := make(chan struct{})
	removed := make(chan string, 1)
	h.e.Container = env.Container{RT: blockingCreate{FakeRuntime: &env.FakeRuntime{}, release: release, removed: removed}}
	h.e.ConformanceKey = conformanceKey
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := h.e.QualifyGuest(ctx, genericACP()); err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("hung create qualified: %v", err)
	}
	close(release)
	select {
	case <-removed:
	case <-time.After(2 * time.Second):
		t.Fatal("late container left running")
	}
}

// blockingCreate holds container creation until release closes and
// reports removals.
type blockingCreate struct {
	*env.FakeRuntime
	release chan struct{}
	removed chan string
}

func (b blockingCreate) Remove(id string) error {
	b.removed <- id
	return b.FakeRuntime.Remove(id)
}

func (b blockingCreate) CreateAndStart(spec env.Spec) (string, error) {
	<-b.release
	return b.FakeRuntime.CreateAndStart(spec)
}

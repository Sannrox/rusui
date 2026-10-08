package engine_test

import (
	"encoding/json"
	"testing"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/guest"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
	"gopkg.in/yaml.v3"
)

func TestSessionGuestChoiceFrozenAndRevoked(t *testing.T) {
	h := setup(t)
	var config policy.File
	if err := yaml.Unmarshal(h.e.PolicySnapshot().Raw, &config); err != nil {
		t.Fatal(err)
	}
	config.Guests = guest.Builtin()
	p := config.Projects["test"]
	p.Guests = &policy.ProjectGuests{Default: "shikigami", Allowed: []string{"shikigami", "claude"}}
	config.Projects["test"] = p
	load := func() {
		t.Helper()
		raw, err := yaml.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.e.ReloadPolicyBytes(raw); err != nil {
			t.Fatal(err)
		}
	}
	original := config.Guests["shikigami"]
	original.Image = "guest:frozen"
	config.Guests["shikigami"] = original
	load()
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil {
		t.Fatal(err)
	}
	if sess.GuestName != "shikigami" || sess.GuestPin != "acp" {
		t.Fatalf("session %+v", sess)
	}
	var captured guest.Entry
	if err := json.Unmarshal([]byte(sess.GuestConfig), &captured); err != nil {
		t.Fatal(err)
	}
	entry := config.Guests["shikigami"]
	entry.Argv = []string{"changed-guest", "acp"}
	entry.Pin = "next"
	entry.Image = "guest:new"
	config.Guests["shikigami"] = entry
	load()
	name, frozen, err := h.e.SessionGuest(sid)
	if err != nil || name != "shikigami" || frozen.Argv[0] != captured.Argv[0] || frozen.Pin != captured.Pin {
		t.Fatalf("frozen %s %+v %v", name, frozen, err)
	}
	runtime := &env.FakeRuntime{}
	h.e.Container = env.Container{RT: runtime, Image: "guest:operator"}
	claim, err := h.e.Claim("example/test-repo")
	if err != nil || claim == nil {
		t.Fatalf("claim frozen guest: %v", err)
	}
	if len(runtime.Created) != 1 || runtime.Created[0].Image != "guest:frozen" {
		t.Fatalf("frozen guest image not used: %+v", runtime.Created)
	}
	if _, err := h.e.Complete(claim.Job.ID, claim.Job.LeaseGeneration, claim.Job.ClaimedRevision, runArt(claim)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.StartRun("test", "queued before revocation", ""); err != nil {
		t.Fatal(err)
	}
	chosen, err := h.e.StartRunGuest("test", "second", "", "", "", "claude")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.GetSession(h.st, chosen)
	if err != nil || other.GuestName != "claude" {
		t.Fatalf("explicit guest %+v %v", other, err)
	}
	before, err := store.ListSessions(h.st, "test", 50)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.StartRunGuest("test", "refused", "", "", "", "codex"); err == nil {
		t.Fatal("unlisted guest accepted")
	}
	after, err := store.ListSessions(h.st, "test", 50)
	if err != nil || len(after) != len(before) {
		t.Fatalf("refused session stored: %d -> %d %v", len(before), len(after), err)
	}
	p = config.Projects["test"]
	p.Guests = &policy.ProjectGuests{Default: "claude", Allowed: []string{"claude"}}
	config.Projects["test"] = p
	load()
	if _, _, err := h.e.SessionGuest(sid); err == nil {
		t.Fatal("revoked guest remained available")
	}
	next, err := h.e.Claim("example/test-repo")
	if err != nil || next == nil {
		t.Fatalf("revoked guest blocked allowed work: %v", err)
	}
	turn, err := store.GetTurn(h.st, next.Job.ID)
	if err != nil || turn.SessionID != chosen {
		t.Fatalf("selected wrong guest session: %+v %v", turn, err)
	}
}

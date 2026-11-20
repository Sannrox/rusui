package store

import (
	"errors"
	"testing"
	"time"
)

func TestParseShareTTL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want time.Duration
		ok   bool
	}{
		{"", time.Hour, true},
		{"1h", time.Hour, true},
		{"4h", 4 * time.Hour, true},
		{"24h", 24 * time.Hour, true},
		{"2h", 0, false},
		{"30m", 0, false},
		{"48h", 0, false},
	}
	for _, tc := range cases {
		got, ok := ParseShareTTL(tc.raw)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("%q got %v ok=%v want %v ok=%v", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

func TestShareGrantClassListRevoke(t *testing.T) {
	s := openTestStore(t)
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	expires := now.Add(time.Hour)
	envID, err := InsertEnvironment(s, Environment{
		Name: "share-grant", Driver: "process", State: EnvReady,
		Handle: "ws", ExpiresAt: &expires, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := PutPreviewGrant(s, PreviewGrant{
		TokenHash: "op", SessionID: 1, EnvironmentID: envID,
		Handle: "ws", Port: 3000, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := GetPreviewGrant(s, "op")
	if err != nil || !ok || got.Class != PreviewClassOperator || got.OperatorGeneration != 0 {
		t.Fatalf("operator default %+v ok=%v err=%v", got, ok, err)
	}
	if err := PutPreviewGrant(s, PreviewGrant{
		TokenHash: "share-live", SessionID: 7, EnvironmentID: envID,
		Handle: "ws", Port: 3000, ServiceName: "web", ExpiresAt: now.Add(time.Hour),
		Class: PreviewClassShare, OperatorGeneration: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := PutPreviewGrant(s, PreviewGrant{
		TokenHash: "share-dead", SessionID: 7, EnvironmentID: envID,
		Handle: "ws", Port: 3000, ServiceName: "web", ExpiresAt: now.Add(-time.Minute),
		Class: PreviewClassShare, OperatorGeneration: 1,
	}); err != nil {
		t.Fatal(err)
	}
	list, err := ListShareGrants(s, 7, now)
	if err != nil || len(list) != 1 || list[0].TokenHash != "share-live" || list[0].ServiceName != "web" {
		t.Fatalf("list %+v err=%v", list, err)
	}
	if err := RevokeShareGrant(s, 7, "missing"); !errors.Is(err, ErrNoRows) {
		t.Fatalf("missing %v", err)
	}
	if err := RevokeShareGrant(s, 7, "op"); !errors.Is(err, ErrNoRows) {
		t.Fatalf("operator hash %v", err)
	}
	if err := RevokeShareGrant(s, 7, "share-live"); err != nil {
		t.Fatal(err)
	}
	list, err = ListShareGrants(s, 7, now)
	if err != nil || len(list) != 0 {
		t.Fatalf("after revoke %+v err=%v", list, err)
	}
	got, ok, err = GetPreviewGrant(s, "share-live")
	if err != nil || !ok || !got.Revoked {
		t.Fatalf("revoked row %+v ok=%v err=%v", got, ok, err)
	}
}

func TestOperatorGenerationRotationRevokesShare(t *testing.T) {
	s := openTestStore(t)
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	expires := now.Add(time.Hour)
	envID, err := InsertEnvironment(s, Environment{
		Name: "share-gen", Driver: "container", State: EnvReady,
		Handle: "ctr-share", ExpiresAt: &expires, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := insertID(s.DB, `INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at)
VALUES (?, 'run', 'example/test-repo', 1, 'issue', 'open', ?) RETURNING id`, envID, now.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	first, err := EnsureOperatorGeneration(s, HashOperatorToken("tok-a"))
	if err != nil || first != 1 {
		t.Fatalf("first gen=%d err=%v", first, err)
	}
	again, err := EnsureOperatorGeneration(s, HashOperatorToken("tok-a"))
	if err != nil || again != 1 {
		t.Fatalf("same hash gen=%d err=%v", again, err)
	}
	if err := PutPreviewGrant(s, PreviewGrant{
		TokenHash: "share-a", SessionID: sessionID, EnvironmentID: envID,
		Handle: "ctr-share", Port: 3000, ServiceName: "web", ExpiresAt: now.Add(time.Hour),
		Class: PreviewClassShare, OperatorGeneration: first,
	}); err != nil {
		t.Fatal(err)
	}
	if err := PutPreviewGrant(s, PreviewGrant{
		TokenHash: "op-a", SessionID: sessionID, EnvironmentID: envID,
		Handle: "ctr-share", Port: 3000, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if reserved, err := ReserveEnvironmentSleep(s, envID, now); err != nil || reserved {
		t.Fatalf("live share allowed sleep: reserved=%v err=%v", reserved, err)
	}
	next, err := EnsureOperatorGeneration(s, HashOperatorToken("tok-b"))
	if err != nil || next != 2 {
		t.Fatalf("rotated gen=%d err=%v", next, err)
	}
	share, ok, err := GetPreviewGrant(s, "share-a")
	if err != nil || !ok || !share.Revoked {
		t.Fatalf("share after rotate %+v ok=%v err=%v", share, ok, err)
	}
	op, ok, err := GetPreviewGrant(s, "op-a")
	if err != nil || !ok || op.Revoked {
		t.Fatalf("operator after rotate %+v ok=%v err=%v", op, ok, err)
	}
	if reserved, err := ReserveEnvironmentSleep(s, envID, now); err != nil || reserved {
		t.Fatalf("operator grant still live: reserved=%v err=%v", reserved, err)
	}
	if err := RevokePreviewGrant(s, "op-a"); err != nil {
		t.Fatal(err)
	}
	if reserved, err := ReserveEnvironmentSleep(s, envID, now); err != nil || !reserved {
		t.Fatalf("revoked grants blocked sleep: reserved=%v err=%v", reserved, err)
	}
}

func TestExpiredShareGrantDoesNotBlockIdleSleep(t *testing.T) {
	s := openTestStore(t)
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	expires := now.Add(time.Hour)
	envID, err := InsertEnvironment(s, Environment{
		Name: "share-exp", Driver: "container", State: EnvReady,
		Handle: "ctr-exp", ExpiresAt: &expires, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := insertID(s.DB, `INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at)
VALUES (?, 'run', 'example/test-repo', 2, 'issue', 'open', ?) RETURNING id`, envID, now.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	if err := PutPreviewGrant(s, PreviewGrant{
		TokenHash: "share-exp", SessionID: sessionID, EnvironmentID: envID,
		Handle: "ctr-exp", Port: 3000, ServiceName: "web", ExpiresAt: now.Add(-time.Second),
		Class: PreviewClassShare, OperatorGeneration: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if reserved, err := ReserveEnvironmentSleep(s, envID, now); err != nil || !reserved {
		t.Fatalf("expired share blocked sleep: reserved=%v err=%v", reserved, err)
	}
}

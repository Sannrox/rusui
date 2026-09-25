package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSleepReservationProtectsTurnsAndLiveGrants(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "environment.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	expires := now.Add(EnvTTL)
	envID, err := InsertEnvironment(s, Environment{
		Name: "sleep-check", Driver: "container", State: EnvReady,
		Handle: "ctr-test", ExpiresAt: &expires, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at)
VALUES (?, 'run', 'example/test-repo', 1, 'issue', 'open', ?)`, envID, now.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	turnRes, err := s.DB.Exec(`INSERT INTO turns (session_id, lane, state) VALUES (?, 'run', 'leased')`, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	turnID, err := turnRes.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if reserved, err := ReserveEnvironmentSleep(s, envID, now); err != nil || reserved {
		t.Fatalf("leased turn allowed sleep: reserved=%v err=%v", reserved, err)
	}
	if _, err := s.DB.Exec(`UPDATE turns SET state='completed' WHERE id=?`, turnID); err != nil {
		t.Fatal(err)
	}
	if err := PutTerminalLease(s, TerminalLease{EnvironmentID: envID, SessionID: sessionID, Generation: 1, ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if reserved, err := ReserveEnvironmentSleep(s, envID, now); err != nil || reserved {
		t.Fatalf("terminal lease allowed sleep: reserved=%v err=%v", reserved, err)
	}
	if err := DeleteTerminalLease(s, envID); err != nil {
		t.Fatal(err)
	}
	if err := PutPreviewGrant(s, PreviewGrant{
		TokenHash: "live-preview", SessionID: sessionID, EnvironmentID: envID,
		Handle: "ctr-test", Port: 3000, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if reserved, err := ReserveEnvironmentSleep(s, envID, now); err != nil || reserved {
		t.Fatalf("preview grant allowed sleep: reserved=%v err=%v", reserved, err)
	}
	if err := RevokePreviewGrant(s, "live-preview"); err != nil {
		t.Fatal(err)
	}
	if reserved, err := ReserveEnvironmentSleep(s, envID, now); err != nil || !reserved {
		t.Fatalf("revoked preview blocked sleep: reserved=%v err=%v", reserved, err)
	}
	if err := PutTerminalLease(s, TerminalLease{EnvironmentID: envID, SessionID: sessionID, Generation: 2, ExpiresAt: now.Add(time.Minute)}); !errors.Is(err, ErrEnvironmentUnavailable) {
		t.Fatalf("terminal lease created during sleep: %v", err)
	}
	if err := PutPreviewGrant(s, PreviewGrant{
		TokenHash: "sleeping-preview", SessionID: sessionID, EnvironmentID: envID,
		Handle: "ctr-test", Port: 3000, ExpiresAt: now.Add(time.Minute),
	}); !errors.Is(err, ErrEnvironmentUnavailable) {
		t.Fatalf("preview grant created during sleep: %v", err)
	}
	list, err := ListSessions(s, "", 10)
	if err != nil || len(list) != 1 || list[0].EnvironmentState != EnvSleeping {
		t.Fatalf("session environment visibility %+v err=%v", list, err)
	}
	if err := InsertEnvironmentReceipt(s, envID, "sleep", "succeeded", "", now); err != nil {
		t.Fatal(err)
	}
	receipts, err := ListEnvironmentReceipts(s, sessionID)
	if err != nil || len(receipts) != 1 || receipts[0].SessionID == nil || *receipts[0].SessionID != sessionID {
		t.Fatalf("environment receipts %+v err=%v", receipts, err)
	}
}

func TestIdleSleepReservationRechecksRecentActivity(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "idle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	ttl, idle := 72*time.Hour, 5*time.Minute
	expires := now.Add(ttl - idle - time.Minute)
	envID, err := InsertEnvironment(s, Environment{
		Name: "idle-race", Driver: "container", State: EnvReady,
		Handle: "ctr-idle", ExpiresAt: &expires, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := ListIdleContainerEnvironments(s, now, ttl, idle)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("idle candidates %+v err=%v", candidates, err)
	}
	refreshed := now.Add(ttl)
	if _, err := s.DB.Exec(`UPDATE environments SET expires_at=? WHERE id=?`, refreshed.Format(time.RFC3339Nano), envID); err != nil {
		t.Fatal(err)
	}
	if reserved, err := ReserveIdleEnvironmentSleep(s, envID, now, ttl, idle); err != nil || reserved {
		t.Fatalf("recently used environment reserved: reserved=%v err=%v", reserved, err)
	}
	got, err := GetEnvironment(s, envID)
	if err != nil || got.State != EnvReady || got.ExpiresAt == nil || !got.ExpiresAt.Equal(refreshed) {
		t.Fatalf("recent environment %+v err=%v", got, err)
	}
}

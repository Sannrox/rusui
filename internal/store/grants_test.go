package store

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"
)

func TestLookupGrantExpiryLeaseAndPrepare(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	prepHash := hashLit("prep")
	if err := PutGrant(st, Grant{TokenHash: prepHash, Kind: GrantPrepare, Repo: "example/test-repo", ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	g, ok, err := LookupGrant(st, prepHash, now)
	if err != nil || !ok || g.CanPush || g.Kind != GrantPrepare {
		t.Fatalf("prepare %+v ok=%v %v", g, ok, err)
	}
	_, ok, err = LookupGrant(st, prepHash, now.Add(2*time.Minute))
	if err != nil || ok {
		t.Fatalf("expired prepare ok=%v %v", ok, err)
	}

	if _, err := st.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1,'review','example/test-repo',1,'issue','open',?)`, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO turns (session_id, lane, state) VALUES (1, 'review', 'leased')`); err != nil {
		t.Fatal(err)
	}
	turnHash := hashLit("turn")
	if err := PutGrant(st, Grant{TokenHash: turnHash, Kind: GrantTurn, SessionID: 1, TurnID: 1, Repo: "example/test-repo", CanPush: true, ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	g, ok, err = LookupGrant(st, turnHash, now)
	if err != nil || !ok || !g.CanPush {
		t.Fatalf("turn %+v ok=%v %v", g, ok, err)
	}
	if _, err := st.DB.Exec(`UPDATE turns SET state='queued' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	_, ok, err = LookupGrant(st, turnHash, now)
	if err != nil || ok {
		t.Fatalf("lease loss still valid ok=%v %v", ok, err)
	}
}

func hashLit(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

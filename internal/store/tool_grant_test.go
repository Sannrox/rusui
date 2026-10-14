package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSpaceToolGrantAndRevoke(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := st.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1,'run','o/r',1,'issue','open',?)`, now); err != nil {
		t.Fatal(err)
	}
	if err := SetSessionExecution(st, 1, ExecutionSpace, "acme"); err != nil {
		t.Fatal(err)
	}
	if ok, err := SessionHasToolGrant(st, 1, "execute"); err != nil || ok {
		t.Fatalf("default grant %v %v", ok, err)
	}
	g, err := GrantSessionTool(st, 1, "execute", "acme")
	if err != nil || g.Tool != "execute" {
		t.Fatalf("grant %+v %v", g, err)
	}
	if _, err := GrantSessionTool(st, 1, "execute", "acme"); !errors.Is(err, ErrToolGrantExists) {
		t.Fatalf("dup %v", err)
	}
	if ok, err := SessionHasToolGrant(st, 1, "execute"); err != nil || !ok {
		t.Fatalf("live %v %v", ok, err)
	}
	if _, err := RevokeSessionTool(st, 1, "execute"); err != nil {
		t.Fatal(err)
	}
	if ok, err := SessionHasToolGrant(st, 1, "execute"); err != nil || ok {
		t.Fatalf("revoked %v %v", ok, err)
	}
	if set := SpaceToolSet(ExecutionSpace, nil); len(set) != 4 {
		t.Fatalf("default set %v", set)
	}
}

func TestUserModeRejectsToolGrant(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := st.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1,'run','o/r',1,'issue','open',?)`, now); err != nil {
		t.Fatal(err)
	}
	if err := SetSessionExecution(st, 1, ExecutionUser, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := GrantSessionTool(st, 1, "write", "alice"); !errors.Is(err, ErrUserModeTools) {
		t.Fatalf("user grant %v", err)
	}
}

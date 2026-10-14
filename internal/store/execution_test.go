package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionExecutionModeStampedAndImmutable(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "e.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := st.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1,'run','o/r',1,'issue','open',?)`, now); err != nil {
		t.Fatal(err)
	}
	if err := SetSessionTenant(st, 1, "acme"); err != nil {
		t.Fatal(err)
	}
	if err := SetSessionExecution(st, 1, ExecutionUser, "alice"); err != nil {
		t.Fatal(err)
	}
	sess, err := GetSession(st, 1)
	if err != nil {
		t.Fatal(err)
	}
	if sess.ExecutionMode != ExecutionUser || sess.Act != "alice" {
		t.Fatalf("got mode=%q act=%q", sess.ExecutionMode, sess.Act)
	}
	if err := ChangeSessionExecution(st, 1, ExecutionSpace, "bob"); !errors.Is(err, ErrExecutionModeImmutable) {
		t.Fatalf("change %v", err)
	}
	sess, err = GetSession(st, 1)
	if err != nil || sess.ExecutionMode != ExecutionUser || sess.Act != "alice" {
		t.Fatalf("mutated %+v %v", sess, err)
	}
}

func TestMigratedSessionDefaultsToSpace(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := st.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1,'run','o/r',1,'issue','open',?)`, now); err != nil {
		t.Fatal(err)
	}
	sess, err := GetSession(st, 1)
	if err != nil {
		t.Fatal(err)
	}
	if sess.ExecutionMode != ExecutionSpace {
		t.Fatalf("mode %q", sess.ExecutionMode)
	}
}

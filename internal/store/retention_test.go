package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestMigratedSessionsDefaultToShortRetention(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var n int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='session_retention'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("session_retention missing: %d %v", n, err)
	}
}

func TestSetAndExpireRetention(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	var sid int64
	err = st.Tx(func(tx *sql.Tx) error {
		var inner error
		sid, _, inner = InsertRunSessionTx(tx, "test", "o/r", "secret prompt")
		if inner != nil {
			return inner
		}
		exp := now.Add(time.Hour)
		return SetSessionRetentionTx(tx, sid, RetentionShort, &exp)
	})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := GetSession(st, sid)
	if err != nil || sess.Retention != RetentionShort || sess.RetentionExpiresAt == nil || sess.Prompt != "secret prompt" {
		t.Fatalf("%+v %v", sess, err)
	}
	due, err := ListDueRetentionSessionIDs(st, now.Add(2*time.Hour))
	if err != nil || len(due) != 1 || due[0] != sid {
		t.Fatalf("due %v %v", due, err)
	}
	err = st.Tx(func(tx *sql.Tx) error {
		if err := ClearSessionDataTx(tx, sid); err != nil {
			return err
		}
		return MarkRetentionExpiredTx(tx, sid)
	})
	if err != nil {
		t.Fatal(err)
	}
	sess, err = GetSession(st, sid)
	if err != nil || sess.Prompt != "" || !sess.RetentionExpired {
		t.Fatalf("cleared %+v %v", sess, err)
	}
}

func TestWriteOverrideRequiresIndefinite(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var sid int64
	err = st.Tx(func(tx *sql.Tx) error {
		var inner error
		sid, _, inner = InsertRunSessionTx(tx, "test", "o/r", "hello")
		return inner
	})
	if err != nil {
		t.Fatal(err)
	}
	err = st.Tx(func(tx *sql.Tx) error {
		return SetRetentionWriteOverrideTx(tx, sid, "alice", time.Now().UTC())
	})
	if !errors.Is(err, ErrRetentionNotIndefinite) {
		t.Fatalf("short override %v", err)
	}
	err = st.Tx(func(tx *sql.Tx) error {
		if err := SetSessionRetentionTx(tx, sid, RetentionIndefinite, nil); err != nil {
			return err
		}
		return SetRetentionWriteOverrideTx(tx, sid, "alice", time.Now().UTC())
	})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := GetSession(st, sid)
	if err != nil || sess.Retention != RetentionIndefinite || !sess.WriteOverride {
		t.Fatalf("%+v %v", sess, err)
	}
	err = st.Tx(func(tx *sql.Tx) error {
		return SetRetentionWriteOverrideTx(tx, sid, "alice", time.Now().UTC())
	})
	if !errors.Is(err, ErrWriteOverrideExists) {
		t.Fatalf("dup override %v", err)
	}
}

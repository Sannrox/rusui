package store

import (
	"database/sql"
	"errors"
	"os"
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
		if err := ClearSessionDataTx(st, tx, sid); err != nil {
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

func TestClassChangeClearsWriteOverride(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var sid int64
	err = st.Tx(func(tx *sql.Tx) error {
		var inner error
		sid, _, inner = InsertRunSessionTx(tx, "test", "o/r", "hello")
		if inner != nil {
			return inner
		}
		if err := SetSessionRetentionTx(tx, sid, RetentionIndefinite, nil); err != nil {
			return err
		}
		return SetRetentionWriteOverrideTx(tx, sid, "alice", time.Now().UTC())
	})
	if err != nil {
		t.Fatal(err)
	}
	err = st.Tx(func(tx *sql.Tx) error {
		exp := time.Now().UTC().Add(EnvTTL)
		return SetSessionRetentionTx(tx, sid, RetentionShort, &exp)
	})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := GetSession(st, sid)
	if err != nil || sess.WriteOverride || sess.Retention != RetentionShort {
		t.Fatalf("after short %+v %v", sess, err)
	}
	err = st.Tx(func(tx *sql.Tx) error {
		return SetSessionRetentionTx(tx, sid, RetentionIndefinite, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	sess, err = GetSession(st, sid)
	if err != nil || sess.WriteOverride || sess.Retention != RetentionIndefinite {
		t.Fatalf("override survived class round-trip %+v %v", sess, err)
	}
}

func TestExpiredRetentionCannotChangeClass(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var sid int64
	err = st.Tx(func(tx *sql.Tx) error {
		var inner error
		sid, _, inner = InsertRunSessionTx(tx, "test", "o/r", "hello")
		if inner != nil {
			return inner
		}
		return MarkRetentionExpiredTx(tx, sid)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = st.Tx(func(tx *sql.Tx) error {
		return SetSessionRetentionTx(tx, sid, RetentionIndefinite, nil)
	})
	if !errors.Is(err, ErrRetentionExpired) {
		t.Fatalf("class after expire %v", err)
	}
}

func TestClearSessionDataRemovesQueuedPromptAndAttachmentFile(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var sid int64
	var digest string
	err = st.Tx(func(tx *sql.Tx) error {
		var inner error
		sid, _, inner = InsertRunSessionTx(tx, "test", "o/r", "hello")
		if inner != nil {
			return inner
		}
		if _, err := EnqueueQueuedPromptTx(tx, sid, "queued secret"); err != nil {
			return err
		}
		atts, err := InsertPromptAttachmentsTx(st, tx, sid, 1, []PromptFile{{Name: "note.txt", Body: []byte("attached secret")}}, time.Now().UTC())
		if err != nil {
			return err
		}
		digest = atts[0].Digest
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(st.AttachmentFile(digest)); err != nil {
		t.Fatalf("attachment missing before clear: %v", err)
	}
	err = st.Tx(func(tx *sql.Tx) error {
		return ClearSessionDataTx(st, tx, sid)
	})
	if err != nil {
		t.Fatal(err)
	}
	var queued string
	if err := st.DB.QueryRow(`SELECT prompt FROM followup_queue WHERE session_id=?`, sid).Scan(&queued); err != nil || queued != "" {
		t.Fatalf("queued prompt %q %v", queued, err)
	}
	if _, err := os.Stat(st.AttachmentFile(digest)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("attachment file remained: %v", err)
	}
}

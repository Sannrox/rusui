package store

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/store/storetest"
)

// Two plane instances share one store but not a disk (#746): an attachment
// stored through one is readable through the other, and once retention
// removes it, the other instance's cache sync drops its copy.
func TestAttachmentSharedAcrossInstances(t *testing.T) {
	dsn := storetest.DSN(t)
	a := openTestStoreAt(t, dsn)
	b := openTestStoreAt(t, dsn)
	b.Dir = t.TempDir()
	body := []byte("attached across instances")
	var sid int64
	var digest string
	err := a.Tx(func(tx *sql.Tx) error {
		var err error
		if sid, _, err = InsertRunSessionTx(tx, "test", "o/r", "hello"); err != nil {
			return err
		}
		atts, err := InsertPromptAttachmentsTx(a, tx, sid, 1, []PromptFile{{Name: "note.txt", Body: body}}, time.Now().UTC())
		if err != nil {
			return err
		}
		digest = atts[0].Digest
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ListPromptAttachments(b, sid, 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("list on b: %v %v", got, err)
	}
	if read, err := os.ReadFile(got[0].Path); err != nil || !bytes.Equal(read, body) || got[0].Size != len(body) {
		t.Fatalf("b read %q size %d %v", read, got[0].Size, err)
	}
	if err := a.Tx(func(tx *sql.Tx) error { return ClearSessionDataTx(a, tx, sid) }); err != nil {
		t.Fatal(err)
	}
	if err := SyncAttachmentCache(b); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(b.AttachmentFile(digest)); err != nil {
		t.Fatalf("sweep removed a file inside the grace period: %v", err)
	}
	old := time.Now().Add(-2 * attachmentSweepGrace)
	if err := os.Chtimes(b.AttachmentFile(digest), old, old); err != nil {
		t.Fatal(err)
	}
	if err := SyncAttachmentCache(b); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(b.AttachmentFile(digest)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("b kept a removed attachment: %v", err)
	}
}

// A row stored before #746 holds no body; the instance that has the file
// copies it into the store, and a mismatched file is not copied.
func TestSyncAttachmentCacheBackfillsBodies(t *testing.T) {
	st := openTestStore(t)
	body := []byte("pre-upgrade bytes")
	var sid int64
	err := st.Tx(func(tx *sql.Tx) error {
		var err error
		if sid, _, err = InsertRunSessionTx(tx, "test", "o/r", "hello"); err != nil {
			return err
		}
		_, err = InsertPromptAttachmentsTx(st, tx, sid, 1, []PromptFile{{Name: "a.txt", Body: body}, {Name: "b.txt", Body: []byte("tampered")}}, time.Now().UTC())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	atts, err := ListPromptAttachments(st, sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`UPDATE prompt_attachments SET body=? WHERE session_id=?`, []byte{}, sid); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(atts[1].Path, []byte("changed on disk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SyncAttachmentCache(st); err != nil {
		t.Fatal(err)
	}
	for i, want := range [][]byte{body, {}} {
		var stored []byte
		if err := st.DB.QueryRow(`SELECT body FROM prompt_attachments WHERE id=?`, atts[i].ID).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(stored, want) {
			t.Fatalf("row %d body %q, want %q", i, stored, want)
		}
	}
}

package store

import (
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/store/storetest"
)

func TestCopiedDatabaseRestoresSession(t *testing.T) {
	storetest.SQLiteOnly(t, "backup is a SQLite file copy")
	dir := t.TempDir()
	src := filepath.Join(dir, "rusui.db")
	st, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	var sid int64
	err = st.Tx(func(tx *sql.Tx) error {
		id, _, err := InsertRunSessionTx(tx, "test", "example/test-repo", "hello")
		sid = id
		if err != nil {
			return err
		}
		_, err = InsertPromptAttachmentsTx(st, tx, id, 1, []PromptFile{{Name: "note.txt", Body: []byte("kept in the copy")}}, time.Now().UTC())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	// The copy lands in another directory, without the attachment cache.
	dst := filepath.Join(t.TempDir(), "restore.db")
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	_ = in.Close()
	_ = out.Close()
	st2, err := Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	sess, err := GetSession(st2, sid)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Prompt != "hello" || sess.Project != "test" {
		t.Fatalf("restored %+v", sess)
	}
	atts, err := ListPromptAttachments(st2, sid, 0)
	if err != nil || len(atts) != 1 {
		t.Fatalf("restored attachments %v %v", atts, err)
	}
	if b, err := os.ReadFile(atts[0].Path); err != nil || string(b) != "kept in the copy" {
		t.Fatalf("restored attachment %q %v", b, err)
	}
}

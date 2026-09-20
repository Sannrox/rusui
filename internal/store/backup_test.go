package store

import (
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCopiedDatabaseRestoresSession(t *testing.T) {
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
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "restore.db")
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
}

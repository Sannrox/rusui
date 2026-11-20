package store

import (
	"database/sql"
	"testing"
)

func TestSessionPatchRoundTripAndClear(t *testing.T) {
	st := openTestStore(t)
	var sid int64
	patch := []byte("diff --git a/note.txt b/note.txt\n")
	err := st.Tx(func(tx *sql.Tx) error {
		var inner error
		sid, _, inner = InsertRunSessionTx(tx, "test", "o/r", "hello")
		if inner != nil {
			return inner
		}
		return PutSessionPatchTx(tx, sid, patch)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := SessionPatch(st, sid)
	if err != nil || string(got) != string(patch) {
		t.Fatalf("got %q %v", got, err)
	}
	missing, err := SessionPatch(st, sid+1)
	if err != nil || missing != nil {
		t.Fatalf("missing %q %v", missing, err)
	}
	err = st.Tx(func(tx *sql.Tx) error {
		return ClearSessionDataTx(st, tx, sid)
	})
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := SessionPatch(st, sid)
	if err != nil || cleared != nil {
		t.Fatalf("cleared %q %v", cleared, err)
	}
}

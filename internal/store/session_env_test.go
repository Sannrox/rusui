package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestReviewSessionGetsOwnEnvironment(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	var a, a2, b int64
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		a, err = ensureReviewSessionTx(tx, "example/test-repo", 1, "issue")
		if err != nil {
			return err
		}
		a2, err = ensureReviewSessionTx(tx, "example/test-repo", 1, "issue")
		if err != nil {
			return err
		}
		b, err = ensureReviewSessionTx(tx, "example/test-repo", 2, "issue")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if a != a2 {
		t.Fatalf("same item new session %d vs %d", a, a2)
	}
	e1 := envOf(t, s, a)
	e2 := envOf(t, s, b)
	if e1.id == DefaultEnvironmentID || e2.id == DefaultEnvironmentID {
		t.Fatalf("used local env %d %d", e1.id, e2.id)
	}
	if e1.id == e2.id {
		t.Fatal("sessions shared an environment")
	}
	if e1.name == LocalEnvironmentName || e2.name == LocalEnvironmentName {
		t.Fatal("named local")
	}
}

func envOf(t *testing.T, s *Store, sessionID int64) (out struct {
	id   int64
	name string
}) {
	t.Helper()
	if err := s.DB.QueryRow(`SELECT e.id, e.name FROM sessions se JOIN environments e ON e.id=se.environment_id WHERE se.id=?`, sessionID).Scan(&out.id, &out.name); err != nil {
		t.Fatal(err)
	}
	return out
}

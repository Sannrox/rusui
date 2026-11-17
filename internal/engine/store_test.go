package engine

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sannrox/rusui/internal/store"
	"github.com/sannrox/rusui/internal/store/storetest"
)

// migratedSQLite is a SQLite store migrated once per test binary. A fresh
// migration costs about 0.6s under -race, and nearly every test opens a store.
var migratedSQLite = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "rusui-engine-test-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "template.db")
	st, err := store.Open(path)
	if err != nil {
		return nil, err
	}
	if err := st.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
})

// OpenTestStore opens a fresh migrated store for t on the backend storetest
// selects and closes it at cleanup. On SQLite it copies the template; on
// PostgreSQL it migrates a new database and sets Dir for attachments. The
// engine_test package uses it too.
func OpenTestStore(t testing.TB) *store.Store {
	t.Helper()
	dsn := storetest.DSN(t)
	if !storetest.OnPostgres() {
		raw, err := migratedSQLite()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dsn, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if dir, ok := storetest.Dir(dsn); ok {
		st.Dir = dir
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

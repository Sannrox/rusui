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

// OpenSharedTestStores opens n handles on one fresh migrated store, as n
// plane instances sharing a database would. Each handle has its own
// connection pool and is closed at cleanup unless the test closes it first.
func OpenSharedTestStores(t testing.TB, n int) []*store.Store {
	t.Helper()
	dsn := testDSN(t)
	out := make([]*store.Store, n)
	for i := range out {
		out[i] = openTestStoreAt(t, dsn)
	}
	return out
}

// OpenTestStore opens a fresh migrated store for t on the backend storetest
// selects and closes it at cleanup. On SQLite it copies the template; on
// PostgreSQL it migrates a new database and sets Dir for attachments. The
// engine_test package uses it too.
func OpenTestStore(t testing.TB) *store.Store {
	t.Helper()
	return openTestStoreAt(t, testDSN(t))
}

// testDSN returns a fresh store DSN, prepared from the migrated template on
// SQLite.
func testDSN(t testing.TB) string {
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
	return dsn
}

func openTestStoreAt(t testing.TB, dsn string) *store.Store {
	t.Helper()
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

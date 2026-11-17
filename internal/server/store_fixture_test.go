package server

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sannrox/rusui/internal/store"
)

// migratedStore is the bytes of a freshly migrated store file, built once per
// test process. Migrating from scratch costs seconds under -race; reopening a
// copy only checks the schema version. The store uses a rollback journal, so
// the closed main file is the whole database.
var migratedStore = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "rusui-server-store-*")
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

// openStore opens the store at path, seeding it from the migrated template when
// the file does not exist yet. A reopen of an existing path (restart tests)
// opens that file as is. The store closes when the test ends.
func openStore(t *testing.T, path string) *store.Store {
	t.Helper()
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		raw, err := migratedStore()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

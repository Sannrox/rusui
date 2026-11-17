// Package storetest picks the store backend for tests. Set RUSUI_TEST_STORE_URL
// to a PostgreSQL admin database URL and every test gets a fresh PostgreSQL
// database; leave it unset and every test gets a SQLite file in its temp dir.
// It does not import package store, so the store tests can use it too.
package storetest

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// URLEnv names the PostgreSQL admin database URL that switches tests to
// PostgreSQL.
const URLEnv = "RUSUI_TEST_STORE_URL"

// OnPostgres reports whether tests run against PostgreSQL.
func OnPostgres() bool { return os.Getenv(URLEnv) != "" }

// SQLiteOnly skips t on PostgreSQL. reason says why the test is SQLite by
// nature (file copies, SQLite migrations or pragmas, file locks).
func SQLiteOnly(t testing.TB, reason string) {
	t.Helper()
	if OnPostgres() {
		t.Skip("SQLite only: " + reason)
	}
}

// PostgresOnly skips t unless tests run on PostgreSQL.
func PostgresOnly(t testing.TB) {
	t.Helper()
	if !OnPostgres() {
		t.Skip("PostgreSQL only: set " + URLEnv)
	}
}

var (
	adminOnce sync.Once
	adminDB   *sql.DB
	adminErr  error
	// dirs maps a PostgreSQL DSN to the directory that holds its attachment
	// files, so every store opened at that DSN finds them.
	dirs sync.Map
)

// DSN returns a DSN for a fresh, empty store owned by t: a SQLite path in
// t.TempDir(), or a new PostgreSQL database dropped at cleanup.
func DSN(t testing.TB) string {
	t.Helper()
	admin := os.Getenv(URLEnv)
	if admin == "" {
		return filepath.Join(t.TempDir(), "rusui.db")
	}
	adminOnce.Do(func() { adminDB, adminErr = sql.Open("pgx", admin) })
	if adminErr != nil {
		t.Fatal(adminErr)
	}
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("rusui_t_%d_%s", os.Getpid(), hex.EncodeToString(raw))
	if _, err := adminDB.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := adminDB.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
	})
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	dsn := u.String()
	dirs.Store(dsn, t.TempDir())
	return dsn
}

// Dir returns the attachment directory for a PostgreSQL DSN from DSN, which
// the caller assigns to Store.Dir. For a SQLite path it returns "", false:
// store.Open already keeps attachments beside the file.
func Dir(dsn string) (string, bool) {
	dir, ok := dirs.Load(dsn)
	if !ok {
		return "", false
	}
	return dir.(string), true
}

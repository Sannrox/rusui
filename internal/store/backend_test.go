package store

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

// testStoreURLEnv names a PostgreSQL admin database URL. When set, the store
// tests run against PostgreSQL, one fresh database per test.
const testStoreURLEnv = "RUSUI_TEST_STORE_URL"

func testOnPostgres() bool { return os.Getenv(testStoreURLEnv) != "" }

// sqliteOnly skips t on PostgreSQL. reason says why the test is SQLite by
// nature (file copies, SQLite migrations, file locks).
func sqliteOnly(t testing.TB, reason string) {
	t.Helper()
	if testOnPostgres() {
		t.Skip("SQLite only: " + reason)
	}
}

// postgresOnly skips t unless the store tests run on PostgreSQL.
func postgresOnly(t testing.TB) {
	t.Helper()
	if !testOnPostgres() {
		t.Skip("PostgreSQL only: set " + testStoreURLEnv)
	}
}

var (
	adminOnce sync.Once
	adminDB   *sql.DB
	adminErr  error
	// testDirs maps a PostgreSQL test DSN to the directory that holds its
	// attachment files, so a reopened store finds them.
	testDirs sync.Map
)

// newTestDSN returns a DSN for a fresh, empty store owned by t: a SQLite
// path in t.TempDir(), or a new PostgreSQL database dropped at cleanup.
func newTestDSN(t testing.TB) string {
	t.Helper()
	admin := os.Getenv(testStoreURLEnv)
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
	testDirs.Store(dsn, t.TempDir())
	return dsn
}

// openTestStore opens a fresh store for t and closes it at cleanup.
func openTestStore(t testing.TB) *Store {
	t.Helper()
	return openTestStoreAt(t, newTestDSN(t))
}

// openTestStoreAt opens the store at a DSN from newTestDSN, failing t on
// error, and closes it at cleanup.
func openTestStoreAt(t testing.TB, dsn string) *Store {
	t.Helper()
	s, err := openTestDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// openTestDSN opens the store at a DSN from newTestDSN and returns the
// error, for tests that expect Open to refuse.
func openTestDSN(dsn string) (*Store, error) {
	s, err := Open(dsn)
	if err != nil {
		return nil, err
	}
	if dir, ok := testDirs.Load(dsn); ok {
		s.Dir = dir.(string)
	}
	return s, nil
}

// insertID runs an INSERT ... RETURNING id on a database or transaction
// and returns the new id.
func insertID(q interface {
	QueryRow(string, ...any) *sql.Row
}, query string, args ...any) (int64, error) {
	var id int64
	err := q.QueryRow(query, args...).Scan(&id)
	return id, err
}

// tableColumns returns the column names of table on either backend; an
// unknown table has none.
func tableColumns(t testing.TB, s *Store, table string) map[string]bool {
	t.Helper()
	q := `SELECT name FROM pragma_table_info(?)`
	if s.pg {
		q = `SELECT column_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = ?`
	}
	rows, err := s.DB.Query(q, table)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	cols := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return cols
}

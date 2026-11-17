package store

import (
	"database/sql"
	"testing"

	"github.com/sannrox/rusui/internal/store/storetest"
)

// openTestStore opens a fresh store for t and closes it at cleanup.
func openTestStore(t testing.TB) *Store {
	t.Helper()
	return openTestStoreAt(t, storetest.DSN(t))
}

// openTestStoreAt opens the store at a DSN from storetest.DSN, failing t on
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

// openTestDSN opens the store at a DSN from storetest.DSN and returns the
// error, for tests that expect Open to refuse.
func openTestDSN(dsn string) (*Store, error) {
	s, err := Open(dsn)
	if err != nil {
		return nil, err
	}
	if dir, ok := storetest.Dir(dsn); ok {
		s.Dir = dir
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

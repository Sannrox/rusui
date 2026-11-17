package store

import (
	"database/sql"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sannrox/rusui/internal/store/storetest"
)

// tableNames lists tables and views on either backend.
func tableNames(t *testing.T, s *Store) []string {
	t.Helper()
	q := `SELECT name FROM sqlite_master WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%'`
	if s.pg {
		q = `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema()`
	}
	rows, err := s.DB.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// A migration that changes the SQLite schema must change
// schema_postgres.sql the same way. The added rowid columns are the only
// difference.
func TestPostgresBaselineColumnsMatchSQLiteMigrations(t *testing.T) {
	storetest.PostgresOnly(t)
	lite, err := Open(filepath.Join(t.TempDir(), "drift.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lite.Close() })
	pg := openTestStore(t)
	liteTables, pgTables := tableNames(t, lite), tableNames(t, pg)
	if strings.Join(liteTables, ",") != strings.Join(pgTables, ",") {
		t.Fatalf("tables differ\nsqlite:   %v\npostgres: %v", liteTables, pgTables)
	}
	for _, table := range liteTables {
		liteCols, pgCols := tableColumns(t, lite, table), tableColumns(t, pg, table)
		if table == "actions" || table == "receipts" {
			delete(pgCols, "rowid")
		}
		if a, b := sortedKeys(liteCols), sortedKeys(pgCols); strings.Join(a, ",") != strings.Join(b, ",") {
			t.Errorf("%s columns differ\nsqlite:   %v\npostgres: %v", table, a, b)
		}
	}
}

func TestPostgresOlderSchemaIsRefused(t *testing.T) {
	storetest.PostgresOnly(t)
	dsn := storetest.DSN(t)
	st := openTestStoreAt(t, dsn)
	if _, err := st.DB.Exec(`DELETE FROM schema_migrations WHERE version=?`, CurrentSchema); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := openTestDSN(dsn); err == nil || !strings.Contains(err.Error(), "older than this binary") {
		t.Fatalf("got %v", err)
	}
}

func TestPostgresUnversionedTablesAreRefused(t *testing.T) {
	storetest.PostgresOnly(t)
	dsn := storetest.DSN(t)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE unrelated (x BIGINT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := openTestDSN(dsn); err == nil || !strings.Contains(err.Error(), "no schema_migrations") {
		t.Fatalf("got %v", err)
	}
}

// Plane instances starting together on an empty database apply the
// baseline once.
func TestPostgresConcurrentOpenAppliesBaselineOnce(t *testing.T) {
	storetest.PostgresOnly(t)
	dsn := storetest.DSN(t)
	const instances = 4
	stores := make([]*Store, instances)
	errs := make([]error, instances)
	var wg sync.WaitGroup
	for i := range instances {
		wg.Go(func() { stores[i], errs[i] = openTestDSN(dsn) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("instance %d: %v", i, err)
		}
		t.Cleanup(func() { _ = stores[i].Close() })
	}
	var versions, planes, envs int
	if err := stores[0].DB.QueryRow(`SELECT (SELECT COUNT(*) FROM schema_migrations), (SELECT COUNT(*) FROM plane_identity), (SELECT COUNT(*) FROM environments)`).Scan(&versions, &planes, &envs); err != nil {
		t.Fatal(err)
	}
	if versions != CurrentSchema || planes != 1 || envs != 1 {
		t.Fatalf("versions=%d planes=%d environments=%d", versions, planes, envs)
	}
}

// Two serializable transactions that each read what the other writes
// conflict; Tx reruns the aborted one and both commit.
func TestPostgresTxRetriesSerializationFailure(t *testing.T) {
	storetest.PostgresOnly(t)
	st := openTestStore(t)
	var attempts atomic.Int32
	var bothRead sync.WaitGroup
	bothRead.Add(2)
	run := func(read, write string) error {
		first := true
		return st.Tx(func(tx *sql.Tx) error {
			attempts.Add(1)
			var n int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM overlay WHERE key=?`, read).Scan(&n); err != nil {
				return err
			}
			if first {
				first = false
				bothRead.Done()
				bothRead.Wait()
			}
			_, err := tx.Exec(`INSERT INTO overlay (key, value) VALUES (?, ?)`, write, strconv.Itoa(n))
			return err
		})
	}
	errs := make(chan error, 2)
	go func() { errs <- run("skew:a", "skew:b") }()
	go func() { errs <- run("skew:b", "skew:a") }()
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if n := attempts.Load(); n < 3 {
		t.Fatalf("attempts = %d, want a retry", n)
	}
	var rows int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM overlay WHERE key IN ('skew:a', 'skew:b')`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("rows = %d %v", rows, err)
	}
}

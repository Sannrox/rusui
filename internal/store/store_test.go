package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// Two handles on one file, as when local attach writes beside the server.
// A read-then-write transaction must wait for a concurrent writer instead of
// failing with SQLITE_BUSY on lock upgrade.
func TestTxWaitsForConcurrentWriterAcrossHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locks.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	if _, err := a.DB.Exec(`CREATE TABLE lock_probe (x INTEGER)`); err != nil {
		t.Fatal(err)
	}

	read := make(chan struct{})
	release := make(chan struct{})
	readerDone := make(chan error, 1)
	go func() {
		readerDone <- b.Tx(func(tx *sql.Tx) error {
			var n int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM lock_probe`).Scan(&n); err != nil {
				return err
			}
			close(read)
			<-release
			_, err := tx.Exec(`INSERT INTO lock_probe (x) VALUES (1)`)
			return err
		})
	}()
	<-read
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- a.Tx(func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO lock_probe (x) VALUES (2)`)
			return err
		})
	}()
	time.Sleep(100 * time.Millisecond) // let the writer reach its lock wait
	close(release)

	if err := <-readerDone; err != nil {
		t.Fatalf("read-then-write tx: %v", err)
	}
	if err := <-writerDone; err != nil {
		t.Fatalf("concurrent writer tx: %v", err)
	}
	var n int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM lock_probe`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("rows = %d, %v; want 2", n, err)
	}
}

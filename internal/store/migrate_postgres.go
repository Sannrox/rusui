package store

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"fmt"
	"time"
)

// schemaPostgres is the PostgreSQL baseline at CurrentSchema. No PostgreSQL
// migrations exist yet: an empty database gets the baseline, and any other
// version is refused.
//
//go:embed schema_postgres.sql
var schemaPostgres string

// schemaLockKey is the pg_advisory_xact_lock key that serializes schema
// checks and the baseline apply across plane instances.
const schemaLockKey int64 = 0x7275737569 // "rusui"

// migratePostgres applies the baseline to an empty database, or checks that
// the database is at CurrentSchema. It runs in one transaction under an
// advisory lock, so two instances starting together apply it once.
func (s *Store) migratePostgres() error {
	ctx := context.Background()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?)`, schemaLockKey); err != nil {
		return err
	}
	var hasSchema bool
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&hasSchema); err != nil {
		return err
	}
	if hasSchema {
		// Older releases did not create the floor table; an empty
		// database gets it with the baseline below.
		if _, err := tx.ExecContext(ctx, floorSQL); err != nil {
			return err
		}
		var ver int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&ver); err != nil {
			return err
		}
		switch {
		case ver > CurrentSchema:
			if err := checkNewerSchema(tx, ver); err != nil {
				return err
			}
			return tx.Commit()
		case ver < CurrentSchema:
			return fmt.Errorf("store: PostgreSQL schema version %d is older than this binary (%d); no PostgreSQL migrations exist yet", ver, CurrentSchema)
		}
		if err := writeFloor(tx); err != nil {
			return err
		}
		return tx.Commit()
	}
	var tables int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema()`).Scan(&tables); err != nil {
		return err
	}
	if tables > 0 {
		return fmt.Errorf("store: PostgreSQL database has %d tables but no schema_migrations; use an empty database", tables)
	}
	if _, err := tx.ExecContext(ctx, schemaPostgres); err != nil {
		return fmt.Errorf("store: PostgreSQL baseline: %w", err)
	}
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO environments (name, driver, state, created_at) VALUES ('local', 'process', 'ready', ?)`, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO runners (name, kind, state, created_at) VALUES ('local', 'process', 'ready', ?)`, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO plane_identity (id) VALUES (?)`, hex.EncodeToString(raw)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) SELECT v, ? FROM generate_series(1, ?::bigint) AS v`, now, CurrentSchema); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, floorSQL); err != nil {
		return err
	}
	if err := writeFloor(tx); err != nil {
		return err
	}
	return tx.Commit()
}

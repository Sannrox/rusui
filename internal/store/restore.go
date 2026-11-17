package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"
)

// RestoreReport is the inventory of a restored plane database.
type RestoreReport struct {
	Path            string
	Sessions        int
	Turns           int
	LeasesCleared   int
	GrantsDropped   int
	ApprovalRecords int
	Environments    int
	// EnvironmentsReleased counts managed environments whose guest handle
	// was dropped; their sessions provision a new guest on the next turn.
	EnvironmentsReleased int
}

// Restore opens a captured store and strips live authority: a SQLite file,
// or a PostgreSQL URL naming a database restored with the server's own
// tooling. Missing or corrupt files fail; they must not look like a
// successful restore. Historical approval_decisions remain as records, not
// grants for new RPCs. The copy never adopts the source's guests: managed
// environments are expired with a receipt and the plane identity is
// rotated, so a copy beside its source cannot attach or name-collide with
// them. For a URL the report names only the server and database.
func Restore(dsn string) (*Store, RestoreReport, error) {
	where := dsn
	if IsPostgresURL(dsn) {
		u, err := url.Parse(dsn)
		if err != nil {
			// Any parse error may quote part of the password; name none of it.
			return nil, RestoreReport{}, errors.New("restore: malformed store URL (percent-encode the password)")
		}
		// Name the database only: credentials ride in the user info or in
		// query parameters (password, sslpassword).
		where = (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
		if err := hasPostgresSchema(dsn); err != nil {
			return nil, RestoreReport{Path: where}, fmt.Errorf("restore: missing artifact: %w", err)
		}
	} else if st, err := os.Stat(dsn); err != nil {
		return nil, RestoreReport{Path: where}, fmt.Errorf("restore: missing artifact: %w", err)
	} else if st.IsDir() || st.Size() == 0 {
		return nil, RestoreReport{Path: where}, fmt.Errorf("restore: corrupt artifact")
	}
	db, err := Open(dsn)
	if err != nil {
		return nil, RestoreReport{Path: where}, fmt.Errorf("restore: corrupt artifact: %w", err)
	}
	rep, err := sanitizeRestored(db)
	rep.Path = where
	if err != nil {
		_ = db.Close()
		return nil, rep, err
	}
	return db, rep, nil
}

// hasPostgresSchema fails unless the database at dsn already holds a rusui
// schema. Open would give an empty database a fresh one, which must not
// pass for a restored backup.
func hasPostgresSchema(dsn string) error {
	db, err := sql.Open(pgDriverName, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var ok bool
	if err := db.QueryRow(`SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return errors.New("database holds no rusui schema")
	}
	return nil
}

func sanitizeRestored(s *Store) (RestoreReport, error) {
	var r RestoreReport
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&r.Sessions); err != nil {
		return r, err
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM turns`).Scan(&r.Turns); err != nil {
		return r, err
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM environments`).Scan(&r.Environments); err != nil {
		return r, err
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM approval_decisions`).Scan(&r.ApprovalRecords); err != nil {
		return r, err
	}
	res, err := s.DB.Exec(`UPDATE turns SET state='queued', lease_expires_at=NULL WHERE state='leased'`)
	if err != nil {
		return r, err
	}
	n, _ := res.RowsAffected()
	r.LeasesCleared = int(n)
	// A restored copy may name a holder that no longer exists. Expire it so
	// the restored plane claims at once; the claim bumps the generation.
	if _, err := s.DB.Exec(`UPDATE plane_job_leases SET expires_at=0`); err != nil {
		return r, err
	}
	res, err = s.DB.Exec(`DELETE FROM turn_credentials`)
	if err != nil {
		return r, err
	}
	n, _ = res.RowsAffected()
	r.GrantsDropped = int(n)
	res, err = s.DB.Exec(`DELETE FROM credential_grants`)
	if err != nil {
		return r, err
	}
	n, _ = res.RowsAffected()
	r.GrantsDropped += int(n)
	if err := MarkRuntimeObservationsUnknown(s, time.Now().UTC()); err != nil {
		return r, err
	}
	released, err := releaseRestoredGuests(s, time.Now())
	r.EnvironmentsReleased = released
	return r, err
}

// releaseRestoredGuests expires every managed environment that names a guest
// and gives the database a fresh plane identity, in one transaction.
func releaseRestoredGuests(s *Store, now time.Time) (int, error) {
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		return 0, err
	}
	var released int
	err := s.Tx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT id FROM environments WHERE name!=? AND COALESCE(handle,'')!=''`, LocalEnvironmentName)
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := tx.Exec(`UPDATE environments SET state=?, handle='', slept_at=NULL WHERE id=?`, EnvExpired, id); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO environment_receipts (environment_id, session_id, kind, state, detail, created_at)
VALUES (?, (SELECT id FROM sessions WHERE environment_id=? ORDER BY id LIMIT 1), 'expire', 'succeeded', 'restored copy does not adopt the source guest', ?)`,
				id, id, now.UTC().Format(time.RFC3339Nano)); err != nil {
				return err
			}
		}
		released = len(ids)
		_, err = tx.Exec(`UPDATE plane_identity SET id=?`, hex.EncodeToString(raw))
		return err
	})
	return released, err
}

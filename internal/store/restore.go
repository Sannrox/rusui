package store

import (
	"fmt"
	"os"
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
}

// Restore opens a captured SQLite artifact and strips live authority.
// Missing or corrupt files fail; they must not look like a successful restore.
// Historical approval_decisions remain as records, not grants for new RPCs.
func Restore(path string) (*Store, RestoreReport, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, RestoreReport{Path: path}, fmt.Errorf("restore: missing artifact: %w", err)
	}
	if st.IsDir() || st.Size() == 0 {
		return nil, RestoreReport{Path: path}, fmt.Errorf("restore: corrupt artifact")
	}
	db, err := Open(path)
	if err != nil {
		return nil, RestoreReport{Path: path}, fmt.Errorf("restore: corrupt artifact: %w", err)
	}
	rep, err := sanitizeRestored(db)
	rep.Path = path
	if err != nil {
		_ = db.Close()
		return nil, rep, err
	}
	return db, rep, nil
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
	return r, nil
}

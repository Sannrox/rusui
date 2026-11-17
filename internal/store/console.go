package store

// Read models for the operator console (internal/server console pages).

// ReceiptKind is one receipt row as the console lists it; payloads are omitted.
type ReceiptKind struct {
	JobID int64
	Kind  string
}

// ListTenantEnvironments returns up to 50 environments used by tenant's
// sessions, newest first. Only ID, Name, Driver, State and SourceHash are set.
func ListTenantEnvironments(s *Store, tenant string) ([]Environment, error) {
	rows, err := s.DB.Query(`SELECT DISTINCT e.id, e.name, e.driver, e.state, IFNULL(e.source_hash,'') FROM environments e JOIN sessions s ON s.environment_id=e.id WHERE COALESCE(s.tenant_id,'default')=? ORDER BY e.id DESC LIMIT 50`, tenant)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Environment
	for rows.Next() {
		var e Environment
		if err := rows.Scan(&e.ID, &e.Name, &e.Driver, &e.State, &e.SourceHash); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListRunners returns every runner by id. Only ID, Name, Kind and State are set.
func ListRunners(s *Store) ([]Runner, error) {
	rows, err := s.DB.Query(`SELECT id, name, kind, state FROM runners ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Runner
	for rows.Next() {
		var r Runner
		if err := rows.Scan(&r.ID, &r.Name, &r.Kind, &r.State); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListTenantReceiptKinds returns up to 50 receipts on tenant's sessions,
// newest job first.
func ListTenantReceiptKinds(s *Store, tenant string) ([]ReceiptKind, error) {
	rows, err := s.DB.Query(`SELECT r.job_id, r.kind FROM receipts r JOIN turns t ON t.id=r.job_id JOIN sessions s ON s.id=t.session_id WHERE COALESCE(s.tenant_id,'default')=? ORDER BY r.job_id DESC LIMIT 50`, tenant)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ReceiptKind
	for rows.Next() {
		var r ReceiptKind
		if err := rows.Scan(&r.JobID, &r.Kind); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountTenantProjectLeasedTurns counts leased turns of project within tenant.
func CountTenantProjectLeasedTurns(s *Store, project, tenant string) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM turns t JOIN sessions s ON s.id=t.session_id WHERE s.project=? AND t.state='leased' AND COALESCE(s.tenant_id,'default')=?`, project, tenant).Scan(&n)
	return n, err
}

// LatestSessionItemHash returns the item hash of the newest review revision
// on any turn of sessionID; ok is false when the session has none.
func LatestSessionItemHash(s *Store, sessionID int64) (hash string, ok bool) {
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM review_revisions WHERE job_id IN (SELECT id FROM turns WHERE session_id=?)`, sessionID).Scan(&n)
	if n == 0 {
		return "", false
	}
	if err := s.DB.QueryRow(`SELECT item_hash FROM review_revisions WHERE job_id IN (SELECT id FROM turns WHERE session_id=?) ORDER BY id DESC LIMIT 1`, sessionID).Scan(&hash); err != nil {
		return "", false
	}
	return hash, true
}

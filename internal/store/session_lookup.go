package store

import "database/sql"

// Narrow single-row reads of a session's scope. Each returns ErrNoRows when
// the session does not exist.

// SessionArchivedTx reports whether session id is archived.
func SessionArchivedTx(tx *sql.Tx, id int64) (bool, error) {
	var archived bool
	err := tx.QueryRow(`SELECT archived FROM sessions WHERE id=?`, id).Scan(&archived)
	return archived, err
}

// SessionProjectTx reads the project of session id.
func SessionProjectTx(tx *sql.Tx, id int64) (string, error) {
	var project string
	err := tx.QueryRow(`SELECT project FROM sessions WHERE id=?`, id).Scan(&project)
	return project, err
}

// SessionKindProjectTx reads the kind and project of session id.
func SessionKindProjectTx(tx *sql.Tx, id int64) (kind, project string, err error) {
	err = tx.QueryRow(`SELECT kind, project FROM sessions WHERE id=?`, id).Scan(&kind, &project)
	return kind, project, err
}

// SessionProjectArchivedTx reads the project and archived flag of session id.
func SessionProjectArchivedTx(tx *sql.Tx, id int64) (project string, archived bool, err error) {
	var a int
	err = tx.QueryRow(`SELECT project, archived FROM sessions WHERE id=?`, id).Scan(&project, &a)
	return project, a != 0, err
}

// SessionProjectKindArchivedTx reads the project, kind and archived flag of
// session id.
func SessionProjectKindArchivedTx(tx *sql.Tx, id int64) (project, kind string, archived bool, err error) {
	var a int
	err = tx.QueryRow(`SELECT project, kind, archived FROM sessions WHERE id=?`, id).Scan(&project, &kind, &a)
	return project, kind, a != 0, err
}

// SessionSourceTx reads the GitHub source (repo, item) of session id.
func SessionSourceTx(tx *sql.Tx, id int64) (repo string, item int, err error) {
	err = tx.QueryRow(`SELECT repo, item FROM sessions WHERE id=?`, id).Scan(&repo, &item)
	return repo, item, err
}

// ParentFanOutTx reads the parent session id of session id and the parent's
// project; parentID is 0 for a root session.
func ParentFanOutTx(tx *sql.Tx, id int64) (parentID int64, parentProject string, err error) {
	err = tx.QueryRow(`SELECT COALESCE(c.parent_session_id, 0), COALESCE(p.project, '')
FROM sessions c LEFT JOIN sessions p ON p.id=c.parent_session_id WHERE c.id=?`, id).Scan(&parentID, &parentProject)
	return parentID, parentProject, err
}

// ReviewSessionIDTx returns the review session of repo#item.
func ReviewSessionIDTx(tx *sql.Tx, repo string, item int) (int64, error) {
	var id int64
	err := tx.QueryRow(`SELECT id FROM sessions WHERE kind=? AND repo=? AND item=?`, SessionKindReview, repo, item).Scan(&id)
	return id, err
}

// ReviewSessionID returns the review session of repo#item.
func ReviewSessionID(s *Store, repo string, item int) (int64, error) {
	var id int64
	err := s.DB.QueryRow(`SELECT id FROM sessions WHERE kind=? AND repo=? AND item=?`, SessionKindReview, repo, item).Scan(&id)
	return id, err
}

// ReviewSessionTenantTx reads the tenant of the review session of
// repo#item, 'default' when unset.
func ReviewSessionTenantTx(tx *sql.Tx, repo string, item int) (string, error) {
	var tenant string
	err := tx.QueryRow(`SELECT COALESCE(tenant_id,'default') FROM sessions WHERE kind=? AND repo=? AND item=?`, SessionKindReview, repo, item).Scan(&tenant)
	return tenant, err
}

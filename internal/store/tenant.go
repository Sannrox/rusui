package store

import (
	"database/sql"
	"errors"
	"strings"
)

// DefaultTenantID is the isolation key for a single-operator install.
// Existing rows migrate here; RUSUI_OPERATOR_TOKEN is bound to it.
const DefaultTenantID = "default"

func NormalizeTenantID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return DefaultTenantID
	}
	return id
}

func SetSessionTenantTx(tx *sql.Tx, sessionID int64, tenantID string) error {
	_, err := tx.Exec(`UPDATE sessions SET tenant_id=? WHERE id=?`, NormalizeTenantID(tenantID), sessionID)
	return err
}

func SetSessionTenant(s *Store, sessionID int64, tenantID string) error {
	return s.Tx(func(tx *sql.Tx) error {
		return SetSessionTenantTx(tx, sessionID, tenantID)
	})
}

func ReviewSession(s *Store, repo string, item int) (*Session, error) {
	var id int64
	err := s.DB.QueryRow(`SELECT id FROM sessions WHERE kind=? AND repo=? AND item=?`, SessionKindReview, repo, item).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return GetSession(s, id)
}

func FailedReviewItems(s *Store, repo, tenant string) ([]int, error) {
	q := `SELECT s.item FROM turns t JOIN sessions s ON s.id=t.session_id WHERE s.repo=? AND t.lane='review' AND t.state='failed'`
	args := []any{repo}
	if tenant != "" {
		q += ` AND COALESCE(s.tenant_id,'default')=?`
		args = append(args, NormalizeTenantID(tenant))
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var items []int
	for rows.Next() {
		var item int
		if err := rows.Scan(&item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

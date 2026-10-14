package engine

import (
	"database/sql"
	"errors"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

func sessionTenant(p policy.Project) string {
	return store.NormalizeTenantID(p.Tenant)
}

func (e *Engine) stampReviewTenantTx(tx *sql.Tx, repo string, item int) error {
	tenant := store.DefaultTenantID
	if p, ok := e.PolicySnapshot().ProjectForRepo(repo); ok {
		tenant = sessionTenant(p)
	}
	var sid int64
	if err := tx.QueryRow(`SELECT id FROM sessions WHERE kind=? AND repo=? AND item=?`, store.SessionKindReview, repo, item).Scan(&sid); err != nil {
		return err
	}
	if err := store.SetSessionTenantTx(tx, sid, tenant); err != nil {
		return err
	}
	return store.SetSessionExecutionTx(tx, sid, store.ExecutionSpace, "")
}

func (e *Engine) reviewTenantMismatchTx(tx *sql.Tx, repo string, item int) (bool, error) {
	want := store.DefaultTenantID
	if p, ok := e.PolicySnapshot().ProjectForRepo(repo); ok {
		want = sessionTenant(p)
	}
	var got string
	err := tx.QueryRow(`SELECT COALESCE(tenant_id,'default') FROM sessions WHERE kind=? AND repo=? AND item=?`, store.SessionKindReview, repo, item).Scan(&got)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return store.NormalizeTenantID(got) != want, nil
}

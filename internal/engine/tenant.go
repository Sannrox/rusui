package engine

import (
	"errors"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

func sessionTenant(p policy.Project) string {
	return store.NormalizeTenantID(p.Tenant)
}

func (e *Engine) stampReviewTenantTx(tx *store.Tx, repo string, item int) error {
	tenant := store.DefaultTenantID
	if p, ok := e.PolicySnapshot().ProjectForRepo(repo); ok {
		tenant = sessionTenant(p)
	}
	sid, err := store.ReviewSessionIDTx(tx, repo, item)
	if err != nil {
		return err
	}
	if err := store.SetSessionTenantTx(tx, sid, tenant); err != nil {
		return err
	}
	return store.SetSessionExecutionTx(tx, sid, store.ExecutionSpace, "")
}

func (e *Engine) reviewTenantMismatchTx(tx *store.Tx, repo string, item int) (bool, error) {
	want := store.DefaultTenantID
	if p, ok := e.PolicySnapshot().ProjectForRepo(repo); ok {
		want = sessionTenant(p)
	}
	got, err := store.ReviewSessionTenantTx(tx, repo, item)
	if errors.Is(err, store.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return store.NormalizeTenantID(got) != want, nil
}

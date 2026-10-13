package engine_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

func TestStartRunStampsProjectTenant(t *testing.T) {
	st := openMigrated(t, filepath.Join(t.TempDir(), "t.db"))
	t.Cleanup(func() { _ = st.Close() })
	pol, err := policy.Parse([]byte(`
version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
projects:
  acme:
    tenant: acme
    session_kinds: [run]
`))
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock.Fake{T: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)}
	e := engine.New(st, pol, gh.NewFake(), clk)
	e.ReloadPolicy(pol)
	sid, err := e.StartRun("acme", "hello", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(st, sid)
	if err != nil {
		t.Fatal(err)
	}
	if sess.TenantID != "acme" {
		t.Fatalf("tenant %q", sess.TenantID)
	}
	if sess.CreatedBy != "acme" {
		t.Fatalf("created_by %q", sess.CreatedBy)
	}
}

func TestReviewSessionInheritsProjectTenant(t *testing.T) {
	h := setup(t)
	pol, err := policy.Parse([]byte(strings.Replace(fixture, "  test:\n", "  test:\n    tenant: acme\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(pol)
	h.putRefresh(issue(1))
	var sid int64
	if err := h.st.DB.QueryRow(`SELECT id FROM sessions WHERE kind=?`, store.SessionKindReview).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil {
		t.Fatal(err)
	}
	if sess.TenantID != "acme" {
		t.Fatalf("review tenant %q", sess.TenantID)
	}
}

func TestReviewAdmissionStopsAfterTenantReassignment(t *testing.T) {
	h := setup(t)
	pol, err := policy.Parse([]byte(strings.Replace(fixture, "  test:\n", "  test:\n    tenant: acme\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(pol)
	it := issue(1)
	it.ItemKind = "pull"
	it.HeadSHA = "head-1"
	it.BaseRef = "main"
	h.putRefresh(it)
	var sid int64
	var pending int
	if err := h.st.DB.QueryRow(`SELECT s.id, t.pending_revision FROM sessions s JOIN turns t ON t.session_id=s.id WHERE s.kind=? AND s.item=?`, store.SessionKindReview, it.Item).Scan(&sid, &pending); err != nil {
		t.Fatal(err)
	}
	pol, err = policy.Parse([]byte(strings.Replace(fixture, "  test:\n", "  test:\n    tenant: globex\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(pol)
	it.HeadSHA = "head-2"
	it.UpdatedAt = "2026-02-01T00:00:00Z"
	h.f.Put(it)
	if err := h.e.CatchUpItem(it.Repo, it.Item, it.ItemKind); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.StepRefresh(); err != nil {
		t.Fatal(err)
	}
	var pending2 int
	var tenant string
	if err := h.st.DB.QueryRow(`SELECT t.pending_revision, COALESCE(s.tenant_id,'default') FROM sessions s JOIN turns t ON t.session_id=s.id WHERE s.id=?`, sid).Scan(&pending2, &tenant); err != nil {
		t.Fatal(err)
	}
	if tenant != "acme" {
		t.Fatalf("restamped tenant %q", tenant)
	}
	if pending2 != pending {
		t.Fatalf("admitted after reassignment %d -> %d", pending, pending2)
	}
	c, err := h.e.Claim(it.Repo)
	if err != nil {
		t.Fatal(err)
	}
	if c != nil {
		t.Fatalf("claimed reassigned session %+v", c.Job)
	}
}

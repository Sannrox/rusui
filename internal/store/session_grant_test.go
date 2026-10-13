package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionAccessGrantAndRevoke(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := st.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1,'run','o/r',1,'issue','open',?)`, now); err != nil {
		t.Fatal(err)
	}
	if err := SetSessionTenant(st, 1, "acme"); err != nil {
		t.Fatal(err)
	}
	ok, err := SessionGranted(st, 1, "globex")
	if err != nil || ok {
		t.Fatalf("granted before insert ok=%v %v", ok, err)
	}
	if _, err := GrantSessionAccess(st, 1, "acme", "acme"); !errors.Is(err, ErrGrantSelf) {
		t.Fatalf("self grant %v", err)
	}
	g, err := GrantSessionAccess(st, 1, "globex", "acme")
	if err != nil || g.GranteeTenant != "globex" || g.CreatedBy != "acme" {
		t.Fatalf("grant %+v %v", g, err)
	}
	ok, err = SessionGranted(st, 1, "globex")
	if err != nil || !ok {
		t.Fatalf("granted after insert ok=%v %v", ok, err)
	}
	if _, err := GrantSessionAccess(st, 1, "globex", "acme"); !errors.Is(err, ErrGrantExists) {
		t.Fatalf("duplicate %v", err)
	}
	list, err := ListSessionAccessGrants(st, 1)
	if err != nil || len(list) != 1 || list[0].GranteeTenant != "globex" {
		t.Fatalf("list %+v %v", list, err)
	}
	if err := RevokeSessionAccess(st, 1, "globex"); err != nil {
		t.Fatal(err)
	}
	ok, err = SessionGranted(st, 1, "globex")
	if err != nil || ok {
		t.Fatalf("granted after revoke ok=%v %v", ok, err)
	}
	if err := RevokeSessionAccess(st, 1, "globex"); !errors.Is(err, ErrUnknownGrant) {
		t.Fatalf("second revoke %v", err)
	}
	if _, err := GrantSessionAccess(st, 1, "globex", "acme"); err != nil {
		t.Fatalf("regrant %v", err)
	}
}

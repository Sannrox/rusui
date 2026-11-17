package server

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/oidc"
	"github.com/sannrox/rusui/internal/oidc/oidctest"
	"github.com/sannrox/rusui/internal/store"
)

type actClaims struct {
	jwt.RegisteredClaims
	Act map[string]string `json:"act,omitempty"`
}

// ADR 0088: a vouched subject is an operator only as its mapped tenant and
// actor, sees only what that actor sees, and decision rows name the actor
// and delegating service, never the sub.
func TestVouchedSubjectIsScopedAndAttributed(t *testing.T) {
	s, hs, e := consoleEnv(t)
	p := oidctest.New(t)
	v, err := oidc.NewVerifier(t.Context(), oidc.Config{Metadata: oidc.Metadata{Issuer: p.Server.URL, Audience: "rusui", ClientID: "cli"}, Subject: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	s.OIDC = v
	s.OIDCSubjects, err = LoadOIDCSubjects(`{"u-alice":{"tenant":"acme","actor":"alice"},"u-bob":{"tenant":"acme","actor":"bob"},"u-carol":{"tenant":"globex","actor":"carol"}}`, "operator")
	if err != nil {
		t.Fatal(err)
	}
	token := func(sub string) string {
		c := p.Claims()
		c.Subject, c.ExpiresAt = sub, jwt.NewNumericDate(time.Now().Add(30*time.Minute))
		return p.Token(t, actClaims{RegisteredClaims: c, Act: map[string]string{"sub": "svc"}})
	}
	alice, bob, carol := token("u-alice"), token("u-bob"), token("u-carol")

	if code := getStatus(t, hs.URL+"/approvals", token("u-mallory")); code != http.StatusUnauthorized {
		t.Fatalf("unmapped subject: %d", code)
	}

	userID, err := e.StartRunExec("test", "user-session", "", "", "", "", store.ExecutionUser, "alice", "alice")
	if err != nil {
		t.Fatal(err)
	}
	spaceID, err := e.StartRunExec("test", "space-session", "", "", "", "", store.ExecutionSpace, "acme", "alice")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{userID, spaceID} {
		if err := store.SetSessionTenant(e.Store, id, "acme"); err != nil {
			t.Fatal(err)
		}
	}
	approval := func(id string, sess int64) {
		t.Helper()
		if err := store.InsertAction(e.Store, store.Action{
			ID: id, SessionID: &sess, Repo: "example/test-repo", Item: 1,
			Type: acp.ActionApproval, ReasonCode: acp.ReasonUnmatched,
			EvidenceClass: acp.EvidenceObserved, LimitSentence: acp.LimitSentence, Body: `{"tool":"shell"}`,
		}); err != nil {
			t.Fatal(err)
		}
	}
	approval("act-user", userID)
	approval("act-space", spaceID)

	if code := getStatus(t, hs.URL+"/sessions/"+strconv.FormatInt(spaceID, 10), carol); code != http.StatusNotFound {
		t.Fatalf("foreign tenant read session: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+"/approvals/act-user", bob, `{"decision":"allow"}`); code != http.StatusNotFound {
		t.Fatalf("non-act decided user-mode approval: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+"/approvals/act-space", bob, `{"decision":"allow"}`); code != http.StatusNoContent {
		t.Fatalf("tenant actor decided space-mode approval: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+"/approvals/act-user", alice, `{"decision":"deny"}`); code != http.StatusNoContent {
		t.Fatalf("act decided own approval: %d", code)
	}

	row := func(id string) (by, via sql.NullString) {
		t.Helper()
		if err := e.Store.DB.QueryRow(`SELECT decided_by, delegated_by FROM approval_decisions WHERE action_id=?`, id).Scan(&by, &via); err != nil {
			t.Fatal(err)
		}
		return
	}
	if by, via := row("act-space"); by.String != "bob" || via.String != "svc" {
		t.Fatalf("space decision attributed to %v via %v", by, via)
	}
	if by, _ := row("act-user"); by.String != "alice" {
		t.Fatalf("user decision attributed to %v", by)
	}
	var leaked int
	if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM approval_decisions WHERE instr(COALESCE(decided_by,'') || COALESCE(delegated_by,''), 'u-') > 0`).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("decision rows store a raw subject: %d %v", leaked, err)
	}

	// The static operator stays unattributed.
	approval("act-static", spaceID)
	if err := store.SetSessionTenant(e.Store, spaceID, store.DefaultTenantID); err != nil {
		t.Fatal(err)
	}
	if code := postJSONStatus(t, hs.URL+"/approvals/act-static", "op-tok", `{"decision":"allow"}`); code != http.StatusNoContent {
		t.Fatalf("static operator decide: %d", code)
	}
	if by, via := row("act-static"); by.Valid || via.Valid {
		t.Fatalf("static operator attributed: %v %v", by, via)
	}
}

func TestLoadOIDCSubjectsRefusesConflicts(t *testing.T) {
	for _, tc := range []struct{ name, raw, operator string }{
		{"operator subject mapped", `{"operator":{"tenant":"acme","actor":"alice"}}`, "operator"},
		{"pair mapped twice", `{"a":{"tenant":"acme","actor":"alice"},"b":{"tenant":"acme","actor":"alice"}}`, "operator"},
		{"default operator pair", `{"a":{"tenant":"default","actor":"default"}}`, "operator"},
		{"no OIDC issuer", `{"a":{"tenant":"acme","actor":"alice"}}`, ""},
		{"missing actor", `{"a":{"tenant":"acme"}}`, "operator"},
		{"unknown field", `{"a":{"tenant":"acme","actor":"alice","role":"admin"}}`, "operator"},
		{"invalid tenant", `{"a":{"tenant":"Acme","actor":"alice"}}`, "operator"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := LoadOIDCSubjects(tc.raw, tc.operator); err == nil {
				t.Fatal("accepted")
			} else if strings.Contains(err.Error(), `"a"`) || strings.Contains(err.Error(), "operator\"") {
				t.Fatalf("error names a subject: %v", err)
			}
		})
	}
	overlap := &Server{MemberToks: map[string]string{"tok-a": "acme"}, ActorToks: map[string]string{"tok-a": "alice"}}
	overlap.OIDCSubjects = map[string]VouchedSubject{"a": {Tenant: "acme", Actor: "alice"}}
	if overlap.CheckOIDCSubjectOverlap() == nil {
		t.Fatal("subject shares an actor with a tenant token")
	}
	overlap.OIDCSubjects["a"] = VouchedSubject{Tenant: "acme", Actor: "bob"}
	if err := overlap.CheckOIDCSubjectOverlap(); err != nil {
		t.Fatal(err)
	}
	m, err := LoadOIDCSubjects(`{"a":{"tenant":"acme","actor":"alice"}}`, "operator")
	if err != nil || m["a"] != (VouchedSubject{Tenant: "acme", Actor: "alice"}) {
		t.Fatalf("valid map: %v %v", m, err)
	}
}

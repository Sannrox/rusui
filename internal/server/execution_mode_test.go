package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

func TestExecutionModeReturnedAndImmutable(t *testing.T) {
	s, hs, e := consoleEnv(t)
	s.TenantToks = map[string]string{"globex": "tok-globex-secret"}
	s.MemberToks = map[string]string{"tok-alice": "acme", "tok-bob": "acme"}
	s.ActorToks = map[string]string{"tok-alice": "alice", "tok-bob": "bob"}
	userID, err := e.StartRunExec("test", "user-session", "", "", "", "", store.ExecutionUser, "alice", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionTenant(e.Store, userID, "acme"); err != nil {
		t.Fatal(err)
	}
	spaceID, err := e.StartRunExec("test", "space-session", "", "", "", "", store.ExecutionSpace, "acme", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionTenant(e.Store, spaceID, "acme"); err != nil {
		t.Fatal(err)
	}
	userPath := "/sessions/" + strconv.FormatInt(userID, 10)
	spacePath := "/sessions/" + strconv.FormatInt(spaceID, 10)
	raw := getJSON(t, hs.URL+userPath, "tok-alice")
	var wrap struct {
		Session store.Session `json:"session"`
	}
	if err := json.Unmarshal([]byte(raw), &wrap); err != nil {
		t.Fatal(err)
	}
	if wrap.Session.ExecutionMode != store.ExecutionUser || wrap.Session.Act != "alice" {
		t.Fatalf("user session %s", raw)
	}
	req, err := http.NewRequest(http.MethodPut, hs.URL+userPath+"/execution-mode", strings.NewReader(`{"execution_mode":"space"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok-alice")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("change mode %d", res.StatusCode)
	}
	if code := getStatus(t, hs.URL+userPath, "tok-bob"); code != http.StatusNotFound {
		t.Fatalf("bob read alice user session: %d", code)
	}
	idsBob := sessionIDs(t, getJSON(t, hs.URL+"/sessions", "tok-bob"))
	for _, id := range idsBob {
		if id == userID {
			t.Fatalf("list leaked user-mode session: %v", idsBob)
		}
	}
	if code := getStatus(t, hs.URL+spacePath, "tok-bob"); code != http.StatusOK {
		t.Fatalf("bob read space session: %d", code)
	}
	if code := getStatus(t, hs.URL+userPath+"/read", "tok-bob"); code != http.StatusNotFound {
		t.Fatalf("bob transcript: %d", code)
	}
	if code := getStatus(t, hs.URL+userPath+"/logs", "tok-bob"); code != http.StatusNotFound {
		t.Fatalf("bob logs: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+userPath+"/grants", "tok-alice", `{"tenant":"globex"}`); code != http.StatusCreated {
		t.Fatalf("alice grant globex on user-mode: %d", code)
	}
	if code := getStatus(t, hs.URL+userPath, "tok-globex-secret"); code != http.StatusNotFound {
		t.Fatalf("grant leaked user-mode session: %d", code)
	}
	if code := getStatus(t, hs.URL+userPath+"/grants", "tok-bob"); code != http.StatusNotFound {
		t.Fatalf("bob listed alice grants: %d", code)
	}
}

func TestMemberCreatesUserModeSession(t *testing.T) {
	s, hs, e := consoleEnv(t)
	s.MemberToks = map[string]string{"tok-alice": "acme", "tok-bob": "acme"}
	s.ActorToks = map[string]string{"tok-alice": "alice", "tok-bob": "bob"}
	pol, err := policy.Parse([]byte(`version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
projects:
  acme:
    tenant: acme
    repos:
      acme/repo:
        visibility: public
`))
	if err != nil {
		t.Fatal(err)
	}
	e.ReloadPolicy(pol)
	if code := postJSONStatus(t, hs.URL+"/projects/acme/sessions", "tok-alice", `{"kind":"run","prompt":"mine","execution_mode":"user"}`); code != http.StatusCreated {
		t.Fatalf("alice create user-mode: %d", code)
	}
	ids := sessionIDs(t, getJSON(t, hs.URL+"/sessions", "tok-alice"))
	if len(ids) != 1 {
		t.Fatalf("alice list %v", ids)
	}
	raw := getJSON(t, hs.URL+"/sessions/"+strconv.FormatInt(ids[0], 10), "tok-alice")
	var wrap struct {
		Session store.Session `json:"session"`
	}
	if err := json.Unmarshal([]byte(raw), &wrap); err != nil {
		t.Fatal(err)
	}
	if wrap.Session.ExecutionMode != store.ExecutionUser || wrap.Session.Act != "alice" || wrap.Session.CreatedBy != "alice" {
		t.Fatalf("created %s", raw)
	}
	if code := getStatus(t, hs.URL+"/sessions/"+strconv.FormatInt(ids[0], 10), "tok-bob"); code != http.StatusNotFound {
		t.Fatalf("bob read alice created session: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+"/projects/acme/sessions", "tok-alice", `{"kind":"run","prompt":"x","execution_mode":"user","effort_key":"k","repo":"acme/repo","ref":"main","base_sha":"aaa"}`); code != http.StatusUnauthorized {
		t.Fatalf("member pinned create: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+"/projects/acme/sessions", "tok-alice", `{"kind":"run","prompt":"shared","execution_mode":"space"}`); code != http.StatusCreated {
		t.Fatalf("alice create space-mode: %d", code)
	}
	spaceIDs := sessionIDs(t, getJSON(t, hs.URL+"/sessions", "tok-bob"))
	if len(spaceIDs) != 1 {
		t.Fatalf("bob space list %v", spaceIDs)
	}
	spaceRaw := getJSON(t, hs.URL+"/sessions/"+strconv.FormatInt(spaceIDs[0], 10), "tok-bob")
	var spaceWrap struct {
		Session store.Session `json:"session"`
	}
	if err := json.Unmarshal([]byte(spaceRaw), &spaceWrap); err != nil {
		t.Fatal(err)
	}
	if spaceWrap.Session.ExecutionMode != store.ExecutionSpace || spaceWrap.Session.Act != "acme" || spaceWrap.Session.CreatedBy != "alice" {
		t.Fatalf("space session %s", spaceRaw)
	}
}

func TestPinnedAndWorkerRejectUserMode(t *testing.T) {
	_, hs, _ := consoleEnv(t)
	if code := postJSONStatus(t, hs.URL+"/projects/test/sessions", "wsec", `{"kind":"run","prompt":"x","execution_mode":"user"}`); code != http.StatusConflict {
		t.Fatalf("worker user-mode: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+"/projects/test/sessions", "wsec", `{"kind":"run","prompt":"x","execution_mode":"user","effort_key":"k","repo":"example/test-repo","ref":"main","base_sha":"aaa"}`); code != http.StatusConflict {
		t.Fatalf("pinned user-mode: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+"/projects/test/sessions", "wsec", `{"kind":"local","execution_mode":"user"}`); code != http.StatusConflict {
		t.Fatalf("local user-mode: %d", code)
	}
}

func TestLoadTenantTokensNestedActors(t *testing.T) {
	tokens, members, actors, err := LoadTenantTokens(`{"acme":{"alice":"tok-a","bob":"tok-b"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 0 {
		t.Fatalf("simple tokens %v", tokens)
	}
	if members["tok-a"] != "acme" || members["tok-b"] != "acme" {
		t.Fatalf("members %v", members)
	}
	if actors["tok-a"] != "alice" || actors["tok-b"] != "bob" {
		t.Fatalf("actors %v", actors)
	}
}

func TestUserModeHidesApprovalsFromOtherMembers(t *testing.T) {
	s, hs, e := consoleEnv(t)
	s.MemberToks = map[string]string{"tok-alice": "acme", "tok-bob": "acme"}
	s.ActorToks = map[string]string{"tok-alice": "alice", "tok-bob": "bob"}
	userID, err := e.StartRunExec("test", "user-session", "", "", "", "", store.ExecutionUser, "alice", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionTenant(e.Store, userID, "acme"); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertAction(e.Store, store.Action{
		ID: "act-alice-secret", SessionID: &userID, Repo: "example/test-repo", Item: 1,
		Type: acp.ActionApproval, ReasonCode: acp.ReasonUnmatched,
		EvidenceClass: acp.EvidenceObserved, LimitSentence: acp.LimitSentence,
		Body: `{"tool":"shell","command":"secret"}`,
	}); err != nil {
		t.Fatal(err)
	}
	listed := getJSON(t, hs.URL+"/approvals", "tok-bob")
	if strings.Contains(listed, "act-alice-secret") {
		t.Fatalf("bob listed alice approval: %s", listed)
	}
	if code := getStatus(t, hs.URL+"/approvals/act-alice-secret", "tok-bob"); code != http.StatusNotFound {
		t.Fatalf("bob get approval: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+"/approvals/act-alice-secret", "tok-bob", `{"decision":"deny"}`); code != http.StatusNotFound {
		t.Fatalf("bob decide: %d", code)
	}
	listedA := getJSON(t, hs.URL+"/approvals", "tok-alice")
	if !strings.Contains(listedA, "act-alice-secret") {
		t.Fatalf("alice missed approval: %s", listedA)
	}
}

func TestIdempotencyDoesNotReuseOtherActorSession(t *testing.T) {
	s, hs, e := consoleEnv(t)
	s.MemberToks = map[string]string{"tok-alice": "acme", "tok-bob": "acme"}
	s.ActorToks = map[string]string{"tok-alice": "alice", "tok-bob": "bob"}
	pol, err := policy.Parse([]byte(`version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
projects:
  acme:
    tenant: acme
    repos:
      acme/repo:
        visibility: public
`))
	if err != nil {
		t.Fatal(err)
	}
	e.ReloadPolicy(pol)
	req, err := http.NewRequest(http.MethodPost, hs.URL+"/projects/acme/sessions", strings.NewReader(`{"kind":"run","prompt":"mine","execution_mode":"user"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok-alice")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "shared-key")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("alice create %d", res.StatusCode)
	}
	req, err = http.NewRequest(http.MethodPost, hs.URL+"/projects/acme/sessions", strings.NewReader(`{"kind":"run","prompt":"other","execution_mode":"user"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok-bob")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "shared-key")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("bob reused alice idempotency: %d", res.StatusCode)
	}
}

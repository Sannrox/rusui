package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

func TestTenantCredentialCannotListReadOrDecideForeignWork(t *testing.T) {
	s, hs, e := consoleEnv(t)
	s.TenantToks = map[string]string{"acme": "tok-acme-secret", "globex": "tok-globex-secret"}

	sidA, err := e.StartRun("test", "tenant-a", "")
	if err != nil {
		t.Fatal(err)
	}
	sidB, err := e.StartRun("test", "tenant-b", "")
	if err != nil {
		t.Fatal(err)
	}
	sidDefault, err := e.StartRun("test", "default-tenant", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionTenant(e.Store, sidA, "acme"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionTenant(e.Store, sidB, "globex"); err != nil {
		t.Fatal(err)
	}

	if err := store.InsertAction(e.Store, store.Action{
		ID: "act-a", SessionID: &sidA, Repo: "example/test-repo", Item: 1,
		Type: acp.ActionApproval, ReasonCode: acp.ReasonUnmatched,
		EvidenceClass: acp.EvidenceObserved, LimitSentence: acp.LimitSentence,
		Body: `{"tool":"shell"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertAction(e.Store, store.Action{
		ID: "act-b", SessionID: &sidB, Repo: "example/test-repo", Item: 2,
		Type: acp.ActionApproval, ReasonCode: acp.ReasonUnmatched,
		EvidenceClass: acp.EvidenceObserved, LimitSentence: acp.LimitSentence,
		Body: `{"tool":"shell"}`,
	}); err != nil {
		t.Fatal(err)
	}

	idsA := sessionIDs(t, getJSON(t, hs.URL+"/sessions", "tok-acme-secret"))
	if slices.Contains(idsA, sidB) || slices.Contains(idsA, sidDefault) {
		t.Fatalf("acme listed foreign session: %v", idsA)
	}
	if !slices.Contains(idsA, sidA) {
		t.Fatalf("acme missed its session: %v", idsA)
	}

	idsDefault := sessionIDs(t, getJSON(t, hs.URL+"/sessions", "op-tok"))
	if slices.Contains(idsDefault, sidA) || slices.Contains(idsDefault, sidB) {
		t.Fatalf("default operator listed foreign session: %v", idsDefault)
	}
	if !slices.Contains(idsDefault, sidDefault) {
		t.Fatalf("default operator missed its session: %v", idsDefault)
	}

	if code := getStatus(t, hs.URL+"/sessions/"+strconv.FormatInt(sidB, 10), "tok-acme-secret"); code != http.StatusNotFound {
		t.Fatalf("acme read globex session: %d", code)
	}
	if code := getStatus(t, hs.URL+"/sessions/"+strconv.FormatInt(sidA, 10), "tok-acme-secret"); code != http.StatusOK {
		t.Fatalf("acme read own session: %d", code)
	}
	if code := getStatus(t, hs.URL+"/sessions/"+strconv.FormatInt(sidB, 10)+"/read", "tok-acme-secret"); code != http.StatusNotFound {
		t.Fatalf("acme read globex transcript: %d", code)
	}
	if code := getStatus(t, hs.URL+"/sessions/"+strconv.FormatInt(sidA, 10)+"/read", "tok-acme-secret"); code != http.StatusOK && code != http.StatusConflict {
		t.Fatalf("acme read own transcript: %d", code)
	}
	if code := getStatus(t, hs.URL+"/sessions/"+strconv.FormatInt(sidB, 10)+"/logs", "tok-acme-secret"); code != http.StatusNotFound {
		t.Fatalf("acme read globex logs: %d", code)
	}

	approvalsA := getJSON(t, hs.URL+"/approvals", "tok-acme-secret")
	if strings.Contains(approvalsA, "act-b") {
		t.Fatalf("acme listed globex approval: %s", approvalsA)
	}
	if !strings.Contains(approvalsA, "act-a") {
		t.Fatalf("acme missed its approval: %s", approvalsA)
	}

	if code := postJSONStatus(t, hs.URL+"/approvals/act-b", "tok-acme-secret", `{"decision":"deny"}`); code != http.StatusNotFound {
		t.Fatalf("acme decided globex approval: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+"/approvals/act-a", "tok-acme-secret", `{"decision":"deny"}`); code != http.StatusNoContent {
		t.Fatalf("acme decide own approval: %d", code)
	}

	idsWorker := sessionIDs(t, getJSON(t, hs.URL+"/sessions", "wsec"))
	if !slices.Contains(idsWorker, sidA) || !slices.Contains(idsWorker, sidB) {
		t.Fatalf("worker should see every tenant: %v", idsWorker)
	}
}

func TestTenantGrantAllowsReopenAndRevoke(t *testing.T) {
	s, hs, e := consoleEnv(t)
	s.TenantToks = map[string]string{"acme": "tok-acme-secret", "globex": "tok-globex-secret"}
	sidA, err := e.StartRun("test", "grant-me", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionTenant(e.Store, sidA, "acme"); err != nil {
		t.Fatal(err)
	}
	path := "/sessions/" + strconv.FormatInt(sidA, 10)
	if code := getStatus(t, hs.URL+path, "tok-globex-secret"); code != http.StatusNotFound {
		t.Fatalf("globex read before grant: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+path+"/unarchive", "tok-globex-secret", `{}`); code != http.StatusNotFound {
		t.Fatalf("globex reopen before grant: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+path+"/grants", "tok-globex-secret", `{"tenant":"globex"}`); code != http.StatusNotFound {
		t.Fatalf("globex granted itself: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+path+"/grants", "tok-acme-secret", `{"tenant":"acme"}`); code != http.StatusBadRequest {
		t.Fatalf("acme grant self: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+path+"/grants", "tok-acme-secret", `{"tenant":"globex"}`); code != http.StatusCreated {
		t.Fatalf("acme grant globex: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+path+"/grants", "tok-acme-secret", `{"tenant":"globex"}`); code != http.StatusConflict {
		t.Fatalf("duplicate grant: %d", code)
	}
	if !strings.Contains(getJSON(t, hs.URL+path+"/grants", "tok-acme-secret"), `"tenant":"globex"`) {
		t.Fatal("owner grant list missing globex")
	}
	if code := getStatus(t, hs.URL+path+"/grants", "tok-globex-secret"); code != http.StatusNotFound {
		t.Fatalf("grantee listed grants: %d", code)
	}
	idsGlobex := sessionIDs(t, getJSON(t, hs.URL+"/sessions", "tok-globex-secret"))
	if slices.Contains(idsGlobex, sidA) {
		t.Fatalf("list leaked granted session: %v", idsGlobex)
	}
	if code := getStatus(t, hs.URL+path, "tok-globex-secret"); code != http.StatusOK {
		t.Fatalf("globex read after grant: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+path+"/unarchive", "tok-globex-secret", `{}`); code != http.StatusNoContent && code != http.StatusOK && code != http.StatusConflict {
		t.Fatalf("globex reopen after grant: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+path+"/cancel", "tok-globex-secret", `{}`); code != http.StatusNotFound {
		t.Fatalf("globex cancelled via grant: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+path+"/archive", "tok-globex-secret", `{}`); code != http.StatusNotFound {
		t.Fatalf("globex archived via grant: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+path+"/turns", "tok-globex-secret", `{"prompt":"nope"}`); code != http.StatusNotFound {
		t.Fatalf("globex prompted via grant: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+path+"/grants", "tok-acme-secret", `{"tenant":"default"}`); code != http.StatusCreated {
		t.Fatalf("acme grant default: %d", code)
	}
	if code := getStatus(t, hs.URL+path, "op-tok"); code != http.StatusOK {
		t.Fatalf("default read after grant: %d", code)
	}
	op := operatorClient(t, hs)
	page, err := op.Get(hs.URL + "/console/sessions/" + strconv.FormatInt(sidA, 10))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, page.Body)
	_ = page.Body.Close()
	if page.StatusCode != http.StatusOK {
		t.Fatalf("console read after default grant: %d", page.StatusCode)
	}
	if code := postJSONStatus(t, hs.URL+path+"/turns", "op-tok", `{"prompt":"nope"}`); code != http.StatusNotFound {
		t.Fatalf("default prompted via grant: %d", code)
	}
	form := url.Values{"csrf": {s.consoleCSRF()}, "mode": {"follow_up"}, "prompt": {"nope"}}
	req, err := http.NewRequest(http.MethodPost, hs.URL+"/console/sessions/"+strconv.FormatInt(sidA, 10)+"/prompt", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer op-tok")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("console prompt via grant: %d", res.StatusCode)
	}
	if code := deleteStatus(t, hs.URL+path+"/grants/globex", "tok-acme-secret"); code != http.StatusNoContent {
		t.Fatalf("acme revoke: %d", code)
	}
	if code := getStatus(t, hs.URL+path, "tok-globex-secret"); code != http.StatusNotFound {
		t.Fatalf("globex read after revoke: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+path+"/unarchive", "tok-globex-secret", `{}`); code != http.StatusNotFound {
		t.Fatalf("globex reopen after revoke: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+path+"/grants", "tok-acme-secret", `{"tenant":"globex"}`); code != http.StatusCreated {
		t.Fatalf("regrant after revoke: %d", code)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	att, err := http.NewRequestWithContext(ctx, http.MethodGet, hs.URL+path+"/attach", nil)
	if err != nil {
		t.Fatal(err)
	}
	att.Header.Set("Authorization", "Bearer tok-globex-secret")
	done := make(chan error, 1)
	go func() {
		res, err := http.DefaultClient.Do(att)
		if err != nil {
			done <- err
			return
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		done <- nil
	}()
	time.Sleep(300 * time.Millisecond)
	if code := deleteStatus(t, hs.URL+path+"/grants/globex", "tok-acme-secret"); code != http.StatusNoContent {
		t.Fatalf("revoke live stream: %d", code)
	}
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			t.Fatalf("attach after revoke: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("attach continued after revoke")
	}
}

func sessionIDs(t *testing.T, raw string) []int64 {
	t.Helper()
	var list []store.Session
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatalf("sessions json: %v %s", err, raw)
	}
	ids := make([]int64, 0, len(list))
	for _, sess := range list {
		ids = append(ids, sess.ID)
	}
	return ids
}

func TestTenantCredentialCannotMutateForeignReviewOrMeasurements(t *testing.T) {
	s, hs, e := consoleEnv(t)
	s.TenantToks = map[string]string{"acme": "tok-acme-secret", "globex": "tok-globex-secret"}
	pol, err := policy.Parse([]byte(`version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
  review: true
projects:
  acme:
    tenant: acme
    repos:
      acme/repo:
        visibility: public
        review: true
  globex:
    tenant: globex
    repos:
      globex/repo:
        visibility: public
        review: true
  test:
    repos:
      example/test-repo:
        visibility: public
`))
	if err != nil {
		t.Fatal(err)
	}
	e.ReloadPolicy(pol)

	if code := postJSONStatus(t, hs.URL+"/reviews", "tok-acme-secret", `{"repo":"globex/repo","item":7}`); code != http.StatusNotFound {
		t.Fatalf("acme review globex: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+"/retry", "tok-acme-secret", `{"target":"globex/repo#7"}`); code != http.StatusNotFound {
		t.Fatalf("acme retry globex: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+"/reviews", "wsec", `{"repo":"globex/repo","item":7}`); code == http.StatusNotFound || code == http.StatusUnauthorized {
		t.Fatalf("worker review globex blocked: %d", code)
	}

	legacyIns, err := e.Store.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1, ?, 'acme/repo', 9, 'pull', 'open', '2026-01-01T00:00:00Z')`, store.SessionKindReview)
	if err != nil {
		t.Fatal(err)
	}
	legacy, _ := legacyIns.LastInsertId()
	if err := store.SetSessionTenant(e.Store, legacy, "globex"); err != nil {
		t.Fatal(err)
	}
	if code := postJSONStatus(t, hs.URL+"/reviews", "tok-acme-secret", `{"repo":"acme/repo","item":9}`); code != http.StatusNotFound {
		t.Fatalf("acme review reassigned globex session: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+"/retry", "tok-acme-secret", `{"target":"acme/repo#9"}`); code != http.StatusNotFound {
		t.Fatalf("acme retry reassigned globex session: %d", code)
	}

	sidA, err := e.StartRun("acme", "tenant-a", "")
	if err != nil {
		t.Fatal(err)
	}
	sidB, err := e.StartRun("globex", "tenant-b", "")
	if err != nil {
		t.Fatal(err)
	}
	turnA, err := e.Store.DB.Exec(`INSERT INTO turns (session_id, lane, state) VALUES (?, 'review', 'completed')`, sidA)
	if err != nil {
		t.Fatal(err)
	}
	tidA, _ := turnA.LastInsertId()
	revA, err := e.Store.DB.Exec(`INSERT INTO review_revisions (job_id, claimed_revision, item_hash, main_sha, payload) VALUES (?, 1, 'h', 'm', '{}')`, tidA)
	if err != nil {
		t.Fatal(err)
	}
	idA, _ := revA.LastInsertId()
	turnB, err := e.Store.DB.Exec(`INSERT INTO turns (session_id, lane, state) VALUES (?, 'review', 'completed')`, sidB)
	if err != nil {
		t.Fatal(err)
	}
	tidB, _ := turnB.LastInsertId()
	revB, err := e.Store.DB.Exec(`INSERT INTO review_revisions (job_id, claimed_revision, item_hash, main_sha, payload) VALUES (?, 1, 'h', 'm', '{}')`, tidB)
	if err != nil {
		t.Fatal(err)
	}
	idB, _ := revB.LastInsertId()
	useful := `{"disposition":"useful"}`
	if code := postJSONStatus(t, hs.URL+"/results/"+strconv.FormatInt(idB, 10)+"/dispositions", "tok-acme-secret", useful); code != http.StatusNotFound {
		t.Fatalf("acme disposed globex: %d", code)
	}
	if code := postJSONStatus(t, hs.URL+"/results/"+strconv.FormatInt(idA, 10)+"/dispositions", "tok-acme-secret", useful); code != http.StatusCreated {
		t.Fatalf("acme disposed own: %d", code)
	}

	if _, err := e.Store.DB.Exec(`INSERT INTO turn_measurements (turn_id, session_id, project, terminal_state) VALUES (?, ?, 'acme', 'completed')`, tidA, sidA); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Store.DB.Exec(`INSERT INTO turn_measurements (turn_id, session_id, project, terminal_state) VALUES (?, ?, 'globex', 'completed')`, tidB, sidB); err != nil {
		t.Fatal(err)
	}
	sidMixed, err := e.StartRun("acme", "reassigned", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionTenant(e.Store, sidMixed, "globex"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Store.DB.Exec(`INSERT INTO turn_measurements (turn_id, session_id, project, terminal_state) VALUES (?, ?, 'acme', 'completed')`, tidB+100, sidMixed); err != nil {
		t.Fatal(err)
	}
	if code := getStatus(t, hs.URL+"/projects/globex/measurements", "tok-acme-secret"); code != http.StatusNotFound {
		t.Fatalf("acme read globex measurements: %d", code)
	}
	body := getJSON(t, hs.URL+"/projects/acme/measurements", "tok-acme-secret")
	var agg store.Aggregates
	if err := json.Unmarshal([]byte(body), &agg); err != nil {
		t.Fatalf("measurements json: %v %s", err, body)
	}
	seen := map[int64]bool{}
	for _, m := range agg.Measurements {
		seen[m.SessionID] = true
	}
	if seen[sidB] || seen[sidMixed] {
		t.Fatalf("acme measurements leaked foreign session: %s", body)
	}
	if !seen[sidA] {
		t.Fatalf("acme missed own measurement: %s", body)
	}
}

func TestConsoleTablesHideForeignTenant(t *testing.T) {
	_, hs, e := consoleEnv(t)
	env, err := e.Store.DB.Exec(`INSERT INTO environments (name, driver, state, created_at) VALUES ('globex-hidden-env', 'process', 'ready', '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	envID, _ := env.LastInsertId()
	sess, err := e.Store.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, project, created_at) VALUES (?, 'run', 'globex/repo', 1, 'issue', 'open', 'globex', '2026-01-01T00:00:00Z')`, envID)
	if err != nil {
		t.Fatal(err)
	}
	sid, _ := sess.LastInsertId()
	if err := store.SetSessionTenant(e.Store, sid, "globex"); err != nil {
		t.Fatal(err)
	}
	turn, err := e.Store.DB.Exec(`INSERT INTO turns (session_id, lane, state) VALUES (?, 'run', 'completed')`, sid)
	if err != nil {
		t.Fatal(err)
	}
	tid, _ := turn.LastInsertId()
	if _, err := e.Store.DB.Exec(`INSERT INTO receipts (job_id, lease_generation, claimed_revision, kind, payload) VALUES (?, 1, 1, 'globex-hidden-receipt', '{}')`, tid); err != nil {
		t.Fatal(err)
	}
	pol, err := policy.Parse([]byte(strings.Replace(slackPol, "  test:\n", "  globex:\n    tenant: globex\n    repos: {}\n  test:\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	e.ReloadPolicy(pol)

	c := operatorClient(t, hs)
	read := func(path string) string {
		t.Helper()
		res, err := c.Get(hs.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s %d %s", path, res.StatusCode, body)
		}
		return string(body)
	}
	if strings.Contains(read("/console/environments"), "globex-hidden-env") {
		t.Fatal("console environments listed globex")
	}
	if strings.Contains(read("/console/receipts"), "globex-hidden-receipt") {
		t.Fatal("console receipts listed globex")
	}
	if strings.Contains(read("/console/budgets"), "globex") {
		t.Fatal("console budgets listed globex project")
	}
}

func TestLoadTenantTokensRejectsDefaultAndOverlap(t *testing.T) {
	if _, err := LoadTenantTokens(`{"default":"tok"}`); err == nil {
		t.Fatal("default tenant accepted")
	}
	s := &Server{OperatorTok: "op", WorkerSec: "wsec", TenantToks: map[string]string{"acme": "op"}}
	if err := s.CheckTenantTokenOverlap(); err == nil {
		t.Fatal("operator overlap accepted")
	}
	if _, err := LoadTenantTokens(`{"acme":"shared-token-value","globex":"shared-token-value"}`); err == nil {
		t.Fatal("duplicate tenant token accepted")
	}
}

func getJSON(t *testing.T, url, token string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%s %d %s", url, res.StatusCode, body)
	}
	if !json.Valid(body) && !strings.Contains(string(body), "[") {
		t.Fatalf("not json %s", body)
	}
	return string(body)
}

func getStatus(t *testing.T, url, token string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	return res.StatusCode
}

func deleteStatus(t *testing.T, url, token string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	return res.StatusCode
}

func postJSONStatus(t *testing.T, url, token, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	return res.StatusCode
}

package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/store"
)

func TestConsoleOpsViewsAndApproval(t *testing.T) {
	s, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "ops", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(e.Store, sid)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertAction(e.Store, store.Action{
		ID: "act-ops", SessionID: &sid, Repo: sess.Repo, Item: sess.Item,
		Type: acp.ActionApproval, ReasonCode: acp.ReasonUnmatched,
		EvidenceClass: acp.EvidenceObserved, LimitSentence: acp.LimitSentence,
		Body: `{"tool":"shell"}`,
	}); err != nil {
		t.Fatal(err)
	}
	c := operatorClient(t, hs)
	res, err := c.Get(hs.URL + "/console/health")
	if err != nil {
		t.Fatal(err)
	}
	health, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || strings.Contains(string(health), "wsec") || strings.Contains(string(health), "op-tok") {
		t.Fatalf("health %d %s", res.StatusCode, health)
	}
	if !strings.Contains(string(health), "policy_hash") {
		t.Fatalf("health %s", health)
	}
	res, err = c.Get(hs.URL + "/console/budgets")
	if err != nil {
		t.Fatal(err)
	}
	bud, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(bud), "leased_turns") {
		t.Fatalf("budgets %d %s", res.StatusCode, bud)
	}
	res, err = c.Get(hs.URL + "/console/approvals")
	if err != nil {
		t.Fatal(err)
	}
	inbox, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(inbox), "act-ops") {
		t.Fatalf("inbox %d %s", res.StatusCode, inbox)
	}
	re := regexp.MustCompile(`name="csrf" value="([^"]+)"`)
	m := re.FindStringSubmatch(string(inbox))
	if len(m) < 2 {
		t.Fatalf("csrf %s", inbox)
	}
	req, err := http.NewRequest("POST", "/console/approvals/act-ops", strings.NewReader(url.Values{"csrf": {m[1]}, "decision": {"deny"}}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range c.Jar.Cookies(res.Request.URL) {
		req.AddCookie(ck)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 && rr.Code != 303 {
		t.Fatalf("deny %d %q loc=%s", rr.Code, rr.Body.String(), rr.Header().Get("Location"))
	}
	res, err = c.Get(hs.URL + "/console/approvals")
	if err != nil {
		t.Fatal(err)
	}
	after, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if strings.Contains(string(after), "act-ops") && !strings.Contains(string(after), "stale") {
		t.Fatalf("still pending %s", after)
	}
	req2, err := http.NewRequest("POST", "/console/approvals/act-ops", strings.NewReader(url.Values{"csrf": {m[1]}, "decision": {"allow"}}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req2.Host = "127.0.0.1"
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range c.Jar.Cookies(res.Request.URL) {
		req2.AddCookie(ck)
	}
	rr2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr2, req2)
	if !strings.Contains(rr2.Body.String(), "stale") {
		t.Fatalf("repeat %d %s", rr2.Code, rr2.Body.String())
	}
}

func TestConsoleOpsRevokedWithoutCookie(t *testing.T) {
	_, hs, _ := consoleEnv(t)
	res, err := http.Get(hs.URL + "/console/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.Request.URL.Path == "/console/health" && res.StatusCode == 200 {
		t.Fatal("unauthenticated health")
	}
}

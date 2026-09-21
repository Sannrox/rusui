package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/store"
)

func TestU8OperatorJourney(t *testing.T) {
	s, hs, e := consoleEnv(t)

	res, err := http.Get(hs.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || string(b) != "ok" {
		t.Fatalf("J01 healthz %d %s", res.StatusCode, b)
	}

	req, err := http.NewRequest("POST", hs.URL+"/projects/test/sessions", strings.NewReader(`{"kind":"run","prompt":"u8 journey"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("J03 dispatch %d %s", res.StatusCode, body)
	}
	var created struct {
		SessionID int64 `json:"session_id"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.SessionID == 0 {
		t.Fatalf("dispatch body %s %v", body, err)
	}
	sid := created.SessionID
	id := strconv.FormatInt(sid, 10)
	sessRow, err := store.GetSession(e.Store, sid)
	if err != nil {
		t.Fatal(err)
	}
	envRow, err := store.GetEnvironment(e.Store, sessRow.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	envRow.Handle = t.TempDir()
	envRow.State = store.EnvReady
	if err := store.UpdateEnvironment(e.Store, *envRow); err != nil {
		t.Fatal(err)
	}

	req, _ = http.NewRequest("GET", hs.URL+"/sessions/"+id, nil)
	req.Header.Set("Authorization", "Bearer op-tok")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(got), "u8 journey") {
		t.Fatalf("J04 inspect %d %s", res.StatusCode, got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	req, _ = http.NewRequestWithContext(ctx, "GET", hs.URL+"/sessions/"+id+"/attach", nil)
	req.Header.Set("Authorization", "Bearer wsec")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()

	req, _ = http.NewRequest("POST", hs.URL+"/sessions/"+id+"/turns", strings.NewReader(`{"prompt":"follow-up"}`))
	req.Header.Set("Authorization", "Bearer op-tok")
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	fb, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(fb), "turn_id") {
		t.Fatalf("J06 follow-up %d %s", res.StatusCode, fb)
	}

	if err := store.InsertAction(e.Store, store.Action{
		ID: "u8-deny", SessionID: &sid, Repo: "example/test-repo", Item: -1,
		Type: acp.ActionApproval, ReasonCode: acp.ReasonUnmatched,
		EvidenceClass: acp.EvidenceObserved, LimitSentence: acp.LimitSentence,
		Body: `{"tool":"shell"}`,
	}); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest("POST", hs.URL+"/approvals/u8-deny", strings.NewReader(`{"decision":"deny"}`))
	req.Header.Set("Authorization", "Bearer op-tok")
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("J07 approve %d", res.StatusCode)
	}

	c := operatorClient(t, hs)
	res, err = c.Get(hs.URL + "/console/sessions/" + id)
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(html), "Session "+id) {
		t.Fatalf("J08 console %d %s", res.StatusCode, html)
	}

	p := &acp.HTTPPlane{Base: hs.URL, Token: "op-tok"}
	sess, err := p.GetSession(context.Background(), sid)
	if err != nil || sess.ID != sid {
		t.Fatalf("J09 editor %v %+v", err, sess)
	}

	res, err = c.Get(hs.URL + "/console/sessions/" + id + "/terminal")
	if err != nil {
		t.Fatal(err)
	}
	term, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(term), "Terminal") {
		t.Fatalf("J10 terminal %d %s", res.StatusCode, term)
	}

	prev := httptest.NewServer(s.PreviewHandler())
	t.Cleanup(prev.Close)
	s.PreviewBase = prev.URL
	csrf := csrfFrom(string(html))
	form := "csrf=" + csrf + "&port=3000"
	preq, _ := http.NewRequest("POST", "/console/sessions/"+id+"/preview", strings.NewReader(form))
	preq.Host = "127.0.0.1"
	preq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range c.Jar.Cookies(res.Request.URL) {
		preq.AddCookie(ck)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, preq)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "?g=") {
		t.Fatalf("J11 preview %d %s", rr.Code, rr.Body.String())
	}

	dreq, _ := http.NewRequest("POST", hs.URL+"/drain", nil)
	dreq.Header.Set("Authorization", "Bearer wsec")
	res, err = http.DefaultClient.Do(dreq)
	if err != nil {
		t.Fatal(err)
	}
	dbod, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("J12 drain %d %s", res.StatusCode, dbod)
	}

	dst := filepath.Join(t.TempDir(), "restore.db")
	q := fmt.Sprintf("VACUUM INTO '%s'", strings.ReplaceAll(dst, "'", "''"))
	if _, err := e.Store.DB.Exec(q); err != nil {
		t.Fatal(err)
	}
	st2, rep, err := store.Restore(dst)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	if rep.Sessions < 1 {
		t.Fatalf("J13 restore %+v", rep)
	}
	gotSess, err := store.GetSession(st2, sid)
	if err != nil || gotSess.ID != sid || gotSess.Kind != store.SessionKindRun {
		t.Fatalf("restore session %+v %v", gotSess, err)
	}
}

func TestU8HealthzIsNotReadyz(t *testing.T) {
	_, hs, _ := consoleEnv(t)
	res, err := http.Get(hs.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("healthz")
	}
	res, err = http.Get(hs.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode == 200 && strings.Contains(string(b), "wsec") {
		t.Fatal("secret leaked")
	}
}

package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

func consoleEnv(t *testing.T) (*Server, *httptest.Server, *engine.Engine) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	p, err := policy.Parse([]byte(slackPol))
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock.Fake{T: time.Unix(1_700_000_000, 0).UTC()}
	e := engine.New(st, p, gh.NewFake(), clk)
	e.Env = env.Process{Root: t.TempDir()}
	e.ReloadPolicy(p)
	s := &Server{Eng: e, WorkerSec: "wsec", OperatorTok: "op-tok", Addr: "127.0.0.1:8080"}
	hs := httptest.NewServer(s.Handler())
	t.Cleanup(hs.Close)
	return s, hs, e
}

func operatorClient(t *testing.T, hs *httptest.Server) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Jar: jar}
	res, err := c.PostForm(hs.URL+"/console/signin", url.Values{"token": {"op-tok"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusSeeOther {
		t.Fatalf("signin %d", res.StatusCode)
	}
	return c
}

func TestConsoleDisabledWithoutOperatorToken(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	p, err := policy.Parse([]byte(slackPol))
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(st, p, gh.NewFake(), &clock.Fake{T: time.Unix(1, 0).UTC()})
	hs := httptest.NewServer((&Server{Eng: e, WorkerSec: "wsec"}).Handler())
	t.Cleanup(hs.Close)
	res, err := http.Get(hs.URL + "/console/sessions")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("code %d", res.StatusCode)
	}
}

func TestConsoleDeniesWorkerSecret(t *testing.T) {
	_, hs, _ := consoleEnv(t)
	req, _ := http.NewRequest("GET", hs.URL+"/console/sessions", nil)
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode == 200 && strings.Contains(string(body), "<h1>Sessions</h1>") {
		t.Fatal("worker listed sessions")
	}
	if res.Request.URL.Path == "/console/sessions" && res.StatusCode == 200 {
		t.Fatal("worker reached sessions")
	}
}

func TestConsoleSignInListsAndDetails(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", `<script>alert(1)</script>`, "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(e.Store, sid)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertAction(e.Store, store.Action{
		ID: "act-1", SessionID: &sid, Repo: sess.Repo, Item: sess.Item,
		Type: acp.ActionApproval, ReasonCode: "blocked_reason",
		EvidenceClass: acp.EvidenceObserved, LimitSentence: acp.LimitSentence,
		Body: "hello transcript",
	}); err != nil {
		t.Fatal(err)
	}
	envRow, err := store.GetEnvironment(e.Store, sess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	envRow.Handle = ws
	envRow.State = store.EnvReady
	if err := store.UpdateEnvironment(e.Store, *envRow); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "README"), []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "blob.bin"), []byte{0, 1, 2}, 0o600); err != nil {
		t.Fatal(err)
	}
	c := operatorClient(t, hs)
	res, err := c.Get(hs.URL + "/console/sessions")
	if err != nil {
		t.Fatal(err)
	}
	list, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(list), "Sessions") || !strings.Contains(string(list), "Skip to content") {
		t.Fatalf("list %d %s", res.StatusCode, list)
	}
	if !strings.Contains(string(list), `href="/console/sessions/`) {
		t.Fatal("no session link")
	}
	res, err = c.Get(hs.URL + "/console/sessions/" + strconv.FormatInt(sid, 10))
	if err != nil {
		t.Fatal(err)
	}
	detail, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	html := string(detail)
	if res.StatusCode != 200 {
		t.Fatalf("detail %d %s", res.StatusCode, detail)
	}
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Fatal("unescaped prompt")
	}
	if !strings.Contains(html, "hello transcript") || !strings.Contains(html, "blocked_reason") {
		t.Fatalf("transcript %s", html)
	}
	if !strings.Contains(html, `sse-connect="/console/sessions/`) {
		t.Fatal("missing sse")
	}
	res, err = c.Get(hs.URL + "/console/sessions/" + strconv.FormatInt(sid, 10) + "/files?path=README")
	if err != nil {
		t.Fatal(err)
	}
	fileb, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(fileb), "ok") {
		t.Fatalf("file %d %s", res.StatusCode, fileb)
	}
	res, err = c.Get(hs.URL + "/console/sessions/" + strconv.FormatInt(sid, 10) + "/files?path=README&view=diff")
	if err != nil {
		t.Fatal(err)
	}
	diffb, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(diffb), "b/README") || !strings.Contains(string(diffb), "/dev/null") {
		t.Fatalf("diff %s", diffb)
	}
	res, err = c.Get(hs.URL + "/console/sessions/" + strconv.FormatInt(sid, 10) + "/files?path=blob.bin")
	if err != nil {
		t.Fatal(err)
	}
	binb, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(binb), "binary") {
		t.Fatalf("binary %s", binb)
	}
	res, err = c.Get(hs.URL + "/console/sessions/" + strconv.FormatInt(sid, 10) + "/files?path=missing")
	if err != nil {
		t.Fatal(err)
	}
	miss, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(miss), "missing") {
		t.Fatalf("missing %s", miss)
	}
}

func TestSessionsAndConsoleExposeEnvironmentStateAndReceipts(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "show sleep", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(e.Store, sid)
	if err != nil {
		t.Fatal(err)
	}
	envRow, err := store.GetEnvironment(e.Store, sess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	envRow.State = store.EnvSleeping
	if err := store.UpdateEnvironment(e.Store, *envRow); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertEnvironmentReceipt(e.Store, envRow.ID, "sleep", "succeeded", "idle timeout", time.Now()); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{}
	listReq, _ := http.NewRequest(http.MethodGet, hs.URL+"/sessions", nil)
	listReq.Header.Set("Authorization", "Bearer wsec")
	listRes, err := client.Do(listReq)
	if err != nil {
		t.Fatal(err)
	}
	listBody, _ := io.ReadAll(listRes.Body)
	_ = listRes.Body.Close()
	var sessions []store.Session
	if listRes.StatusCode != http.StatusOK || json.Unmarshal(listBody, &sessions) != nil {
		t.Fatalf("sessions list %d %s", listRes.StatusCode, listBody)
	}
	var listed *store.Session
	for i := range sessions {
		if sessions[i].ID == sid {
			listed = &sessions[i]
			break
		}
	}
	if listed == nil || listed.EnvironmentState != store.EnvSleeping {
		t.Fatalf("listed session %+v", listed)
	}
	detailReq, _ := http.NewRequest(http.MethodGet, hs.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"?include=receipts", nil)
	detailReq.Header.Set("Authorization", "Bearer op-tok")
	detailRes, err := client.Do(detailReq)
	if err != nil {
		t.Fatal(err)
	}
	detailBody, _ := io.ReadAll(detailRes.Body)
	_ = detailRes.Body.Close()
	var detail struct {
		Session          store.Session              `json:"session"`
		EnvironmentState string                     `json:"environment_state"`
		Receipts         []store.EnvironmentReceipt `json:"environment_receipts"`
	}
	if detailRes.StatusCode != http.StatusOK || json.Unmarshal(detailBody, &detail) != nil || detail.Session.EnvironmentState != store.EnvSleeping || detail.EnvironmentState != store.EnvSleeping || len(detail.Receipts) != 1 || detail.Receipts[0].State != "succeeded" {
		t.Fatalf("session detail %d %s", detailRes.StatusCode, detailBody)
	}

	operator := operatorClient(t, hs)
	res, err := operator.Get(hs.URL + "/console/sessions")
	if err != nil {
		t.Fatal(err)
	}
	listHTML, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(listHTML), "environment sleeping") {
		t.Fatalf("console list %d %s", res.StatusCode, listHTML)
	}
	res, err = operator.Get(hs.URL + "/console/sessions/" + strconv.FormatInt(sid, 10))
	if err != nil {
		t.Fatal(err)
	}
	detailHTML, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(detailHTML), "Environment activity") || !strings.Contains(string(detailHTML), "sleep succeeded") || !strings.Contains(string(detailHTML), "idle timeout") {
		t.Fatalf("console detail %d %s", res.StatusCode, detailHTML)
	}
}

func TestConsoleEventsReconnect(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "stream", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := store.GetSession(e.Store, sid)
	_ = store.InsertAction(e.Store, store.Action{
		ID: "e1", SessionID: &sid, Repo: sess.Repo, Item: sess.Item,
		Type: "note", ReasonCode: "", EvidenceClass: acp.EvidenceObserved,
		LimitSentence: acp.LimitSentence, Body: "first",
	})
	c := operatorClient(t, hs)
	readOnce := func() string {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "GET", hs.URL+"/console/sessions/"+strconv.FormatInt(sid, 10)+"/events", nil)
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		return string(b)
	}
	a := readOnce()
	b := readOnce()
	if !strings.Contains(a, "e-e1") || !strings.Contains(b, "e-e1") {
		t.Fatalf("reconnect %q %q", a, b)
	}
}

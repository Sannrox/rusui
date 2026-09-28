package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

// Operator terminal, preview, and prompt each wake the same sleeping
// environment, run resume and services once, and attribute the wake (#330).
func TestOperatorActionsWakeSleepingEnvironment(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "guest-ok")
	}))
	t.Cleanup(backend.Close)
	s, hs, e := consoleEnv(t)
	rt := &env.FakeRuntime{
		DefaultFiles:    map[string]bool{env.ResumePath: true},
		DefaultContents: map[string][]byte{env.ServicesRusuiPath: []byte("services:\n  web:\n    command: pnpm dev\n")},
		GuestAddrs:      map[string]string{"ctr-1/3000": backend.Listener.Addr().String()},
	}
	e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	prev := httptest.NewServer(s.PreviewHandler())
	t.Cleanup(prev.Close)
	s.PreviewBase = prev.URL

	sid, err := e.StartRun("test", "wake", "")
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.ProvisionEnvironment(engine.EnvSpec{Name: "box-wake", Kind: env.KindContainer})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionEnvironment(e.Store, sid, created.ID); err != nil {
		t.Fatal(err)
	}
	endTurns := func() {
		t.Helper()
		if _, err := e.Store.DB.Exec(`UPDATE turns SET state='failed' WHERE session_id=?`, sid); err != nil {
			t.Fatal(err)
		}
	}
	endTurns()
	sleep := func() {
		t.Helper()
		if _, err := e.SleepEnvironment(created.ID); err != nil {
			t.Fatal(err)
		}
	}
	count := func(match string) int {
		n := 0
		for _, ex := range rt.Execs {
			if ex[len(ex)-1] == match {
				n++
			}
		}
		return n
	}
	failedStarts := 0
	expectAwake := func(cause string, wakes int) {
		t.Helper()
		got, err := store.GetEnvironment(e.Store, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		sess, _ := store.GetSession(e.Store, sid)
		if got.State != store.EnvReady || got.Handle != created.Handle || sess.EnvironmentID != created.ID {
			t.Fatalf("%s: env %+v session env %d", cause, got, sess.EnvironmentID)
		}
		if len(rt.Started) != wakes+failedStarts || count(env.ResumePath) != wakes || count("pnpm dev") != wakes+1 {
			t.Fatalf("%s: starts %d resumes %d services %d, want %d wakes", cause, len(rt.Started), count(env.ResumePath), count("pnpm dev"), wakes)
		}
		receipts, err := store.ListEnvironmentReceipts(e.Store, sid)
		if err != nil {
			t.Fatal(err)
		}
		last := receipts[len(receipts)-1]
		if last.Kind != "wake" || last.State != "succeeded" || last.Detail != cause {
			t.Fatalf("%s: receipt %+v", cause, last)
		}
	}
	c := operatorClient(t, hs)
	page := func() string {
		t.Helper()
		res, err := c.Get(hs.URL + "/console/sessions/" + strconv.FormatInt(sid, 10) + "/terminal")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("terminal page %d %s", res.StatusCode, b)
		}
		return string(b)
	}

	// Terminal observe.
	sleep()
	if html := page(); strings.Contains(html, "environment sleeping") {
		t.Fatal("terminal page still reports sleeping")
	}
	expectAwake("operator terminal", 1)

	// rusui prompt (follow-up).
	sleep()
	req, _ := http.NewRequest(http.MethodPost, hs.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/turns", strings.NewReader(`{"prompt":"next"}`))
	req.Header.Set("Authorization", "Bearer op-tok")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("prompt %d %s", res.StatusCode, body)
	}
	expectAwake("operator prompt", 2)
	endTurns()

	mint := func() *httptest.ResponseRecorder {
		t.Helper()
		csrf := csrfFrom(page())
		req, _ := http.NewRequest(http.MethodPost, "/console/sessions/"+strconv.FormatInt(sid, 10)+"/preview", strings.NewReader(url.Values{"csrf": {csrf}, "port": {"3000"}}.Encode()))
		req.Host = "127.0.0.1"
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		u, _ := url.Parse(hs.URL + "/console/sessions")
		for _, ck := range c.Jar.Cookies(u) {
			req.AddCookie(ck)
		}
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, req)
		return rr
	}
	grants := func() int {
		var n int
		if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM preview_grants WHERE session_id=?`, sid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// A failed wake is explicit and mints nothing. The page render above
	// would wake, so sleep after taking the CSRF token.
	csrf := csrfFrom(page())
	sleep()
	rt.StartHook = func(string) error { return fmt.Errorf("docker start: no space left") }
	req, _ = http.NewRequest(http.MethodPost, "/console/sessions/"+strconv.FormatInt(sid, 10)+"/preview", strings.NewReader(url.Values{"csrf": {csrf}, "port": {"3000"}}.Encode()))
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	u, _ := url.Parse(hs.URL + "/console/sessions")
	for _, ck := range c.Jar.Cookies(u) {
		req.AddCookie(ck)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "wake failed") {
		t.Fatalf("failed wake %d %s", rr.Code, rr.Body.String())
	}
	if n := grants(); n != 0 {
		t.Fatalf("failed wake minted %d grants", n)
	}
	got, _ := store.GetEnvironment(e.Store, created.ID)
	if got.State != store.EnvSleeping || got.Handle != created.Handle {
		t.Fatalf("after failed wake %+v", got)
	}
	receipts, _ := store.ListEnvironmentReceipts(e.Store, sid)
	if last := receipts[len(receipts)-1]; last.Kind != "wake" || last.State != "failed" {
		t.Fatalf("failed wake receipt %+v", last)
	}

	// Preview mint wakes, and the grant reaches the same environment.
	failedStarts = 1
	rt.StartHook = nil
	rr = mint()
	if rr.Code != http.StatusOK {
		t.Fatalf("mint %d %s", rr.Code, rr.Body.String())
	}
	// The terminal page that supplied the CSRF token woke the environment.
	expectAwake("operator terminal", 3)
	res, err = http.Get(prev.URL + "/?g=" + grantFrom(rr.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || string(body) != "guest-ok" {
		t.Fatalf("proxy %d %s", res.StatusCode, body)
	}
}

// Preview mint wakes a sleeping environment by itself, with its own
// receipt cause.
func TestPreviewMintWakesSleepingEnvironment(t *testing.T) {
	s, hs, e := consoleEnv(t)
	rt := &env.FakeRuntime{GuestAddrs: map[string]string{"ctr-1/3000": "127.0.0.1:1"}}
	e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	prev := httptest.NewServer(s.PreviewHandler())
	t.Cleanup(prev.Close)
	s.PreviewBase = prev.URL
	sid, err := e.StartRun("test", "wake-mint", "")
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.ProvisionEnvironment(engine.EnvSpec{Name: "box-mint", Kind: env.KindContainer})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionEnvironment(e.Store, sid, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Store.DB.Exec(`UPDATE turns SET state='failed' WHERE session_id=?`, sid); err != nil {
		t.Fatal(err)
	}
	c := operatorClient(t, hs)
	res, err := c.Get(hs.URL + "/console/sessions/" + strconv.FormatInt(sid, 10))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	csrf := csrfFrom(string(b))
	if _, err := e.SleepEnvironment(created.ID); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, "/console/sessions/"+strconv.FormatInt(sid, 10)+"/preview", strings.NewReader(url.Values{"csrf": {csrf}, "port": {"3000"}}.Encode()))
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	u, _ := url.Parse(hs.URL + "/console/sessions")
	for _, ck := range c.Jar.Cookies(u) {
		req.AddCookie(ck)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("mint %d %s", rr.Code, rr.Body.String())
	}
	got, _ := store.GetEnvironment(e.Store, created.ID)
	if got.State != store.EnvReady || got.Handle != created.Handle {
		t.Fatalf("env %+v", got)
	}
	receipts, _ := store.ListEnvironmentReceipts(e.Store, sid)
	if last := receipts[len(receipts)-1]; last.Kind != "wake" || last.Detail != "operator preview" {
		t.Fatalf("receipt %+v", last)
	}
}

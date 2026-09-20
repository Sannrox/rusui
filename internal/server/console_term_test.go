package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

func TestTerminalWriteLeaseAndGuestExec(t *testing.T) {
	s, hs, e := consoleEnv(t)
	rt := &env.FakeRuntime{}
	pr, pw := io.Pipe()
	outR, outW := io.Pipe()
	rt.StdioHook = func(handle string, argv, envv []string) (io.WriteCloser, io.ReadCloser, func(), error) {
		for _, x := range envv {
			if strings.Contains(x, "XAI") || strings.Contains(x, "GITHUB") || strings.Contains(x, "TOKEN") {
				t.Errorf("secret in guest env %q", x)
			}
		}
		if handle != "ctr-term" {
			t.Errorf("handle %s", handle)
		}
		if len(argv) == 0 || argv[0] != "/bin/sh" {
			t.Errorf("argv %v", argv)
		}
		return pw, outR, func() { _ = pw.Close(); _ = pr.Close(); _ = outW.Close(); _ = outR.Close() }, nil
	}
	e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	sid, err := e.StartRun("test", "term", "")
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
	envRow.Driver = env.KindContainer
	envRow.Handle = "ctr-term"
	envRow.State = store.EnvReady
	if err := store.UpdateEnvironment(e.Store, *envRow); err != nil {
		t.Fatal(err)
	}

	c := operatorClient(t, hs)
	res, err := c.Get(hs.URL + "/console/sessions/" + itoa64(sid) + "/terminal")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(page), `data-xterm="1"`) {
		t.Fatalf("terminal page %d %s", res.StatusCode, page)
	}
	csrf := csrfFrom(string(page))
	post := func(path, body string) *httptest.ResponseRecorder {
		req, _ := http.NewRequest("POST", path, strings.NewReader(body))
		req.Host = "127.0.0.1"
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, ck := range c.Jar.Cookies(res.Request.URL) {
			req.AddCookie(ck)
		}
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, req)
		return rr
	}
	form := url.Values{"csrf": {csrf}}.Encode()
	rr := post("/console/sessions/"+itoa64(sid)+"/terminal/lease", form)
	if rr.Code != 303 && rr.Code != 200 {
		t.Fatalf("lease %d %s", rr.Code, rr.Body.String())
	}
	rr = post("/console/sessions/"+itoa64(sid)+"/terminal/lease", form)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "write lease held") {
		t.Fatalf("second writer %d %s", rr.Code, rr.Body.String())
	}
	n, err := store.CountTerminalAccess(e.Store, envRow.ID, "refuse")
	if err != nil || n < 1 {
		t.Fatalf("audit refuse %d %v", n, err)
	}
	lease, ok, err := store.GetTerminalLease(e.Store, envRow.ID)
	if err != nil || !ok {
		t.Fatal(err)
	}
	in := url.Values{"csrf": {csrf}, "generation": {itoa64(int64(lease.Generation))}, "data": {"echo hi"}}.Encode()
	go func() { _, _ = io.ReadAll(pr) }()
	rr = post("/console/sessions/"+itoa64(sid)+"/terminal/input", in)
	if rr.Code != 303 && rr.Code != 204 && rr.Code != 200 {
		t.Fatalf("input %d %s", rr.Code, rr.Body.String())
	}
	if len(rt.Stdio) < 1 {
		t.Fatal("guest exec not used")
	}
	rr = post("/console/sessions/"+itoa64(sid)+"/terminal/revoke", form)
	if rr.Code != 303 && rr.Code != 200 {
		t.Fatalf("revoke %d %s", rr.Code, rr.Body.String())
	}
	rr = post("/console/sessions/"+itoa64(sid)+"/terminal/lease", form)
	if rr.Code != 303 && rr.Code != 200 {
		t.Fatalf("reacquire %d %s", rr.Code, rr.Body.String())
	}
}

func TestTerminalExpiredEnvironment(t *testing.T) {
	s, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "term", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := store.GetSession(e.Store, sid)
	envRow, _ := store.GetEnvironment(e.Store, sess.EnvironmentID)
	envRow.State = store.EnvExpired
	envRow.Handle = "gone"
	_ = store.UpdateEnvironment(e.Store, *envRow)
	c := operatorClient(t, hs)
	res, err := c.Get(hs.URL + "/console/sessions/" + itoa64(sid) + "/terminal")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(b), "expired") {
		t.Fatalf("%s", b)
	}
	csrf := csrfFrom(string(b))
	req, _ := http.NewRequest("POST", "/console/sessions/"+itoa64(sid)+"/terminal/lease", strings.NewReader(url.Values{"csrf": {csrf}}.Encode()))
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	u, _ := url.Parse(hs.URL + "/console/sessions")
	for _, ck := range c.Jar.Cookies(u) {
		req.AddCookie(ck)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expired lease %d %s", rr.Code, rr.Body.String())
	}
}

func TestTerminalRejectsWorker(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "term", "")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", hs.URL+"/console/sessions/"+itoa64(sid)+"/terminal", nil)
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.Request.URL.Path == "/console/sessions/"+itoa64(sid)+"/terminal" && res.StatusCode == 200 {
		t.Fatal("worker opened terminal")
	}
}

func csrfFrom(html string) string {
	re := regexp.MustCompile(`name="csrf" value="([^"]+)"`)
	m := re.FindStringSubmatch(html)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func itoa64(n int64) string {
	return strconv.FormatInt(n, 10)
}

package server

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

func TestPreviewGrantIsolatedOrigin(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "app-ok")
	}))
	t.Cleanup(backend.Close)
	_, bport, err := net.SplitHostPort(backend.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(bport)

	s, hs, e := consoleEnv(t)
	prev := httptest.NewServer(s.PreviewHandler())
	t.Cleanup(prev.Close)
	s.PreviewBase = prev.URL
	if !PreviewOriginIsolated(hs.URL, prev.URL) {
		t.Fatal("test origins must differ")
	}

	sid, err := e.StartRun("test", "preview", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := store.GetSession(e.Store, sid)
	envRow, _ := store.GetEnvironment(e.Store, sess.EnvironmentID)
	p, ok := e.Env.(env.Process)
	if !ok {
		t.Fatal("process driver")
	}
	ws := filepath.Join(p.Root, "sess")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	envRow.Handle = ws
	envRow.Driver = env.KindProcess
	envRow.State = store.EnvReady
	_ = store.UpdateEnvironment(e.Store, *envRow)

	c := operatorClient(t, hs)
	res, err := c.Get(hs.URL + "/console/sessions/" + strconv.FormatInt(sid, 10))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	csrf := csrfFrom(string(page))
	req, _ := http.NewRequest("POST", "/console/sessions/"+strconv.FormatInt(sid, 10)+"/preview", strings.NewReader(url.Values{"csrf": {csrf}, "port": {strconv.Itoa(port)}}.Encode()))
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	u, _ := url.Parse(hs.URL + "/console/sessions")
	for _, ck := range c.Jar.Cookies(u) {
		req.AddCookie(ck)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "?g=") {
		t.Fatalf("mint %d %s", rr.Code, rr.Body.String())
	}
	grant := grantFrom(rr.Body.String())
	if grant == "" {
		t.Fatal("no grant")
	}

	res, err = http.Get(prev.URL + "/?g=" + grant)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || string(body) != "app-ok" {
		t.Fatalf("proxy %d %s", res.StatusCode, body)
	}

	req, _ = http.NewRequest("GET", prev.URL+"/?g="+grant, nil)
	req.AddCookie(&http.Cookie{Name: consoleCookie, Value: s.consoleCookieValue(), Path: "/console"})
	req.Header.Set("Authorization", "Bearer op-tok")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("operator on preview %d", res.StatusCode)
	}

	res, err = http.Get(prev.URL + "/?g=" + grant + "&port=1")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong port %d", res.StatusCode)
	}

	envRow.Handle = "replaced"
	_ = store.UpdateEnvironment(e.Store, *envRow)
	res, err = http.Get(prev.URL + "/?g=" + grant)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict || !strings.Contains(string(b), "replaced") {
		t.Fatalf("replaced %d %s", res.StatusCode, b)
	}
}

func TestPreviewProxyStripsGrantFromGuest(t *testing.T) {
	var gotQuery, gotAuth string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, "app-ok")
	}))
	t.Cleanup(backend.Close)
	_, bport, err := net.SplitHostPort(backend.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(bport)

	s, hs, e := consoleEnv(t)
	prev := httptest.NewServer(s.PreviewHandler())
	t.Cleanup(prev.Close)
	s.PreviewBase = prev.URL

	sid, err := e.StartRun("test", "preview-strip", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := store.GetSession(e.Store, sid)
	envRow, _ := store.GetEnvironment(e.Store, sess.EnvironmentID)
	p, ok := e.Env.(env.Process)
	if !ok {
		t.Fatal("process driver")
	}
	ws := filepath.Join(p.Root, "sess")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	envRow.Handle = ws
	envRow.Driver = env.KindProcess
	envRow.State = store.EnvReady
	_ = store.UpdateEnvironment(e.Store, *envRow)

	c := operatorClient(t, hs)
	res, err := c.Get(hs.URL + "/console/sessions/" + strconv.FormatInt(sid, 10))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	csrf := csrfFrom(string(page))
	req, _ := http.NewRequest("POST", "/console/sessions/"+strconv.FormatInt(sid, 10)+"/preview", strings.NewReader(url.Values{"csrf": {csrf}, "port": {strconv.Itoa(port)}}.Encode()))
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	u, _ := url.Parse(hs.URL + "/console/sessions")
	for _, ck := range c.Jar.Cookies(u) {
		req.AddCookie(ck)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	grant := grantFrom(rr.Body.String())
	if rr.Code != 200 || grant == "" {
		t.Fatalf("mint %d %s", rr.Code, rr.Body.String())
	}

	res, err = http.Get(prev.URL + "/app?g=" + grant + "&keep=1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || string(body) != "app-ok" {
		t.Fatalf("proxy %d %s", res.StatusCode, body)
	}
	q, _ := url.ParseQuery(gotQuery)
	if q.Get("g") != "" {
		t.Fatalf("guest saw grant query %q", gotQuery)
	}
	if q.Get("keep") != "1" {
		t.Fatalf("stripped unrelated query %q", gotQuery)
	}
	if gotAuth != "" {
		t.Fatalf("guest saw Authorization %q", gotAuth)
	}

	gotQuery, gotAuth = "", ""
	req, _ = http.NewRequest("GET", prev.URL+"/app?keep=1", nil)
	req.Header.Set("Authorization", "Bearer "+grant)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("bearer grant %d", res.StatusCode)
	}
	if strings.Contains(gotQuery, "g=") {
		t.Fatalf("guest saw g in query %q", gotQuery)
	}
	if gotAuth != "" {
		t.Fatalf("guest saw grant bearer %q", gotAuth)
	}
}

func TestPreviewGrantUsesContainerGuestAddr(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "guest-ok")
	}))
	t.Cleanup(backend.Close)
	s, hs, e := consoleEnv(t)
	rt := &env.FakeRuntime{GuestAddrs: map[string]string{"ctr-1/3000": backend.Listener.Addr().String()}}
	e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	prev := httptest.NewServer(s.PreviewHandler())
	t.Cleanup(prev.Close)
	s.PreviewBase = prev.URL

	sid, err := e.StartRun("test", "preview-ctr", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := store.GetSession(e.Store, sid)
	envRow, _ := store.GetEnvironment(e.Store, sess.EnvironmentID)
	envRow.Handle = "ctr-1"
	envRow.Driver = env.KindContainer
	envRow.State = store.EnvReady
	_ = store.UpdateEnvironment(e.Store, *envRow)

	c := operatorClient(t, hs)
	res, err := c.Get(hs.URL + "/console/sessions/" + strconv.FormatInt(sid, 10))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	csrf := csrfFrom(string(page))
	req, _ := http.NewRequest("POST", "/console/sessions/"+strconv.FormatInt(sid, 10)+"/preview", strings.NewReader(url.Values{"csrf": {csrf}, "port": {"3000"}}.Encode()))
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	u, _ := url.Parse(hs.URL + "/console/sessions")
	for _, ck := range c.Jar.Cookies(u) {
		req.AddCookie(ck)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("mint %d %s", rr.Code, rr.Body.String())
	}
	grant := grantFrom(rr.Body.String())
	res, err = http.Get(prev.URL + "/?g=" + grant)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || string(body) != "guest-ok" {
		t.Fatalf("proxy %d %s", res.StatusCode, body)
	}
}

func TestPreviewGrantAllowsContainerPortMatchingPlaneNumber(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "guest-8080")
	}))
	t.Cleanup(backend.Close)
	s, hs, e := consoleEnv(t)
	s.Addr = "127.0.0.1:8080"
	rt := &env.FakeRuntime{GuestAddrs: map[string]string{"ctr-1/8080": backend.Listener.Addr().String()}}
	e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	prev := httptest.NewServer(s.PreviewHandler())
	t.Cleanup(prev.Close)
	s.PreviewBase = prev.URL
	sid, err := e.StartRun("test", "preview-8080", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := store.GetSession(e.Store, sid)
	envRow, _ := store.GetEnvironment(e.Store, sess.EnvironmentID)
	envRow.Handle = "ctr-1"
	envRow.Driver = env.KindContainer
	envRow.State = store.EnvReady
	_ = store.UpdateEnvironment(e.Store, *envRow)
	c := operatorClient(t, hs)
	res, err := c.Get(hs.URL + "/console/sessions/" + strconv.FormatInt(sid, 10))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	csrf := csrfFrom(string(page))
	req, _ := http.NewRequest("POST", "/console/sessions/"+strconv.FormatInt(sid, 10)+"/preview", strings.NewReader(url.Values{"csrf": {csrf}, "port": {"8080"}}.Encode()))
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	u, _ := url.Parse(hs.URL + "/console/sessions")
	for _, ck := range c.Jar.Cookies(u) {
		req.AddCookie(ck)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("mint %d %s", rr.Code, rr.Body.String())
	}
	grant := grantFrom(rr.Body.String())
	res, err = http.Get(prev.URL + "/?g=" + grant)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || string(body) != "guest-8080" {
		t.Fatalf("proxy %d %s", res.StatusCode, body)
	}
}

func TestPreviewGrantDeniesUnspecifiedPlaneAddr(t *testing.T) {
	s, hs, e := consoleEnv(t)
	s.Addr = "0.0.0.0:8080"
	prev := httptest.NewServer(s.PreviewHandler())
	t.Cleanup(prev.Close)
	s.PreviewBase = prev.URL
	sid, err := e.StartRun("test", "preview-unspec", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := store.GetSession(e.Store, sid)
	envRow, _ := store.GetEnvironment(e.Store, sess.EnvironmentID)
	p := e.Env.(env.Process)
	ws := filepath.Join(p.Root, "sess")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	envRow.Handle = ws
	envRow.Driver = env.KindProcess
	envRow.State = store.EnvReady
	_ = store.UpdateEnvironment(e.Store, *envRow)
	c := operatorClient(t, hs)
	res, err := c.Get(hs.URL + "/console/sessions/" + strconv.FormatInt(sid, 10))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	csrf := csrfFrom(string(page))
	req, _ := http.NewRequest("POST", "/console/sessions/"+strconv.FormatInt(sid, 10)+"/preview", strings.NewReader(url.Values{"csrf": {csrf}, "port": {"8080"}}.Encode()))
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	u, _ := url.Parse(hs.URL + "/console/sessions")
	for _, ck := range c.Jar.Cookies(u) {
		req.AddCookie(ck)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("mint unspecified plane %d %s", rr.Code, rr.Body.String())
	}
}

func TestPreviewGrantDeniesPlaneListenPort(t *testing.T) {
	s, hs, e := consoleEnv(t)
	s.Addr = "127.0.0.1:8080"
	prev := httptest.NewServer(s.PreviewHandler())
	t.Cleanup(prev.Close)
	s.PreviewBase = prev.URL
	sid, err := e.StartRun("test", "preview-plane", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := store.GetSession(e.Store, sid)
	envRow, _ := store.GetEnvironment(e.Store, sess.EnvironmentID)
	p := e.Env.(env.Process)
	ws := filepath.Join(p.Root, "sess")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	envRow.Handle = ws
	envRow.Driver = env.KindProcess
	envRow.State = store.EnvReady
	_ = store.UpdateEnvironment(e.Store, *envRow)
	c := operatorClient(t, hs)
	res, err := c.Get(hs.URL + "/console/sessions/" + strconv.FormatInt(sid, 10))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	csrf := csrfFrom(string(page))
	req, _ := http.NewRequest("POST", "/console/sessions/"+strconv.FormatInt(sid, 10)+"/preview", strings.NewReader(url.Values{"csrf": {csrf}, "port": {"8080"}}.Encode()))
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	u, _ := url.Parse(hs.URL + "/console/sessions")
	for _, ck := range c.Jar.Cookies(u) {
		req.AddCookie(ck)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("mint plane port %d %s", rr.Code, rr.Body.String())
	}
}

func TestPreviewListenerServesCommentBesidePlane(t *testing.T) {
	s, _, _ := consoleEnv(t)
	listen, h, err := s.PreviewListener()
	if err != nil || listen != "" || h != nil {
		t.Fatalf("unset PreviewBase: %q %v %v", listen, h, err)
	}

	s.PreviewBase = "http://127.0.0.1:8090"
	listen, h, err = s.PreviewListener()
	if err != nil {
		t.Fatal(err)
	}
	if listen != "127.0.0.1:8090" {
		t.Fatalf("listen %q", listen)
	}
	req := httptest.NewRequest(http.MethodPost, "/comment?g=deadbeef", strings.NewReader(url.Values{"text": {"x"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", s.PreviewBase)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("preview comment %d", rr.Code)
	}

	s.PreviewBase = "http://127.0.0.1:8080"
	if _, _, err := s.PreviewListener(); err == nil {
		t.Fatal("same origin as plane")
	}
	s.PreviewBase = "http://127.0.0.1"
	if _, _, err := s.PreviewListener(); err == nil {
		t.Fatal("missing port")
	}
	s.PreviewBase = "http://192.0.2.1:8090"
	if _, _, err := s.PreviewListener(); err == nil {
		t.Fatal("non-loopback")
	}
	s.PreviewBase = "https://127.0.0.1:8090"
	if _, _, err := s.PreviewListener(); err == nil {
		t.Fatal("https preview bind")
	}
	s.GuestHTTPSOnly = true
	s.PreviewBase = "http://127.0.0.1:8080"
	if _, _, err := s.PreviewListener(); err == nil {
		t.Fatal("same bind as plane")
	}
	s.GuestHTTPSOnly = false
	s.PreviewBase = "http://:8090"
	if _, _, err := s.PreviewListener(); err == nil {
		t.Fatal("empty host")
	}
	s.Addr = ":8080"
	s.PreviewBase = "http://127.0.0.1:8080"
	if _, _, err := s.PreviewListener(); err == nil {
		t.Fatal("unspecified plane bind same port")
	}
	s.Addr = "0.0.0.0:8080"
	s.PreviewBase = "http://127.0.0.1:8080"
	if _, _, err := s.PreviewListener(); err == nil {
		t.Fatal("wildcard plane bind same port")
	}
	s.Addr = ":8080"
	s.PreviewBase = "http://127.0.0.1:8090"
	listen, h, err = s.PreviewListener()
	if err != nil || listen != "127.0.0.1:8090" || h == nil {
		t.Fatalf("unspecified plane different port: %q %v %v", listen, h, err)
	}
}

func TestPlaneHandlerDoesNotServePreviewComment(t *testing.T) {
	s, _, _ := consoleEnv(t)
	req := httptest.NewRequest(http.MethodPost, "/comment?g=deadbeef", strings.NewReader(url.Values{"text": {"x"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("plane POST /comment %d (preview routes belong on PreviewHandler)", rr.Code)
	}

	prev := httptest.NewRecorder()
	s.PreviewHandler().ServeHTTP(prev, req)
	if prev.Code == http.StatusNotFound {
		t.Fatal("preview mux missing POST /comment")
	}
}

func TestPreviewCommentPostsFollowUp(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "app-ok")
	}))
	t.Cleanup(backend.Close)
	_, bport, err := net.SplitHostPort(backend.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	s, hs, e := consoleEnv(t)
	prev := httptest.NewServer(s.PreviewHandler())
	t.Cleanup(prev.Close)
	s.PreviewBase = prev.URL
	sid, grant := mintPreviewGrant(t, s, hs, e, bport)
	post := func(origin string, v url.Values, cookie bool) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, prev.URL+"/comment?g="+grant, strings.NewReader(v.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if cookie {
			req.AddCookie(&http.Cookie{Name: consoleCookie, Value: s.consoleCookieValue(), Path: "/console"})
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}

	if code := post(prev.URL, url.Values{"text": {"save button"}, "url": {prev.URL + "/settings"}, "selector": {"#save"}}, false); code != http.StatusNoContent {
		t.Fatalf("comment %d", code)
	}
	sess, err := store.GetSession(e.Store, sid)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sess.Prompt, "preview comment") || !strings.Contains(sess.Prompt, "url: ") || !strings.Contains(sess.Prompt, "selector: #save") || !strings.Contains(sess.Prompt, "save button") {
		t.Fatalf("follow-up %q", sess.Prompt)
	}

	readReq, err := http.NewRequest(http.MethodGet, hs.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/read", nil)
	if err != nil {
		t.Fatal(err)
	}
	readReq.Header.Set("Authorization", "Bearer "+s.OperatorTok)
	read, err := http.DefaultClient.Do(readReq)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(read.Body)
	_ = read.Body.Close()
	if read.StatusCode != 200 || !strings.Contains(string(body), "save button") {
		t.Fatalf("read %d %s", read.StatusCode, body)
	}

	nope := url.Values{"text": {"nope"}}
	if code := post(prev.URL, nope, true); code != http.StatusForbidden {
		t.Fatalf("operator cookie %d", code)
	}
	if code := post("", nope, false); code != http.StatusForbidden {
		t.Fatalf("missing origin %d", code)
	}
	if code := post(hs.URL, nope, false); code != http.StatusForbidden {
		t.Fatalf("plane origin %d", code)
	}
	if code := post(prev.URL, url.Values{"text": {strings.Repeat("a", previewCommentCap+1)}}, false); code != http.StatusBadRequest {
		t.Fatalf("oversized %d", code)
	}

	bad, _ := http.NewRequest(http.MethodPost, prev.URL+"/comment?g=deadbeef", strings.NewReader(nope.Encode()))
	bad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	bad.Header.Set("Origin", prev.URL)
	badRes, err := http.DefaultClient.Do(bad)
	if err != nil {
		t.Fatal(err)
	}
	_ = badRes.Body.Close()
	if badRes.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown grant %d", badRes.StatusCode)
	}

	// An unexpired grant stops writing follow-ups once its environment
	// expires or is replaced, matching the proxy.
	envRow, _ := store.GetEnvironment(e.Store, sess.EnvironmentID)
	bound := *envRow
	envRow.State = store.EnvExpired
	_ = store.UpdateEnvironment(e.Store, *envRow)
	if code := post(prev.URL, nope, false); code != http.StatusConflict {
		t.Fatalf("expired environment %d", code)
	}
	bound.Handle = "replaced"
	_ = store.UpdateEnvironment(e.Store, bound)
	if code := post(prev.URL, nope, false); code != http.StatusConflict {
		t.Fatalf("replaced environment %d", code)
	}
	if after, _ := store.GetSession(e.Store, sid); strings.Contains(after.Prompt, "nope") {
		t.Fatalf("refused comment reached session: %q", after.Prompt)
	}
}

func mintPreviewGrant(t *testing.T, s *Server, hs *httptest.Server, e *engine.Engine, bport string) (int64, string) {
	t.Helper()
	port, _ := strconv.Atoi(bport)
	sid, err := e.StartRun("test", "preview-comment", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := store.GetSession(e.Store, sid)
	envRow, _ := store.GetEnvironment(e.Store, sess.EnvironmentID)
	p := e.Env.(env.Process)
	ws := filepath.Join(p.Root, "sess")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	envRow.Handle = ws
	envRow.Driver = env.KindProcess
	envRow.State = store.EnvReady
	_ = store.UpdateEnvironment(e.Store, *envRow)
	c := operatorClient(t, hs)
	res, err := c.Get(hs.URL + "/console/sessions/" + strconv.FormatInt(sid, 10))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	csrf := csrfFrom(string(page))
	req, _ := http.NewRequest("POST", "/console/sessions/"+strconv.FormatInt(sid, 10)+"/preview", strings.NewReader(url.Values{"csrf": {csrf}, "port": {strconv.Itoa(port)}}.Encode()))
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	u, _ := url.Parse(hs.URL + "/console/sessions")
	for _, ck := range c.Jar.Cookies(u) {
		req.AddCookie(ck)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("mint %d %s", rr.Code, rr.Body.String())
	}
	grant := grantFrom(rr.Body.String())
	if grant == "" {
		t.Fatal("no grant")
	}
	return sid, grant
}

func grantFrom(html string) string {
	_, rest, ok := strings.Cut(html, "?g=")
	if !ok {
		return ""
	}
	var out strings.Builder
	for _, r := range rest {
		if r == '"' || r == '<' || r == '&' || r == ' ' {
			break
		}
		out.WriteRune(r)
	}
	return out.String()
}

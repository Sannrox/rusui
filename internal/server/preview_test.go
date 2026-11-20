package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
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
	t.Parallel()
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
	t.Parallel()
	var gotQuery, gotAuth, gotReferer string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotReferer = r.Header.Get("Referer")
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

	// Browsers send the minted document URL, grant included, as Referer.
	req, _ = http.NewRequest("GET", prev.URL+"/app?g="+grant+"&keep=1", nil)
	req.Header.Set("Referer", prev.URL+"/?g="+grant+"&tab=2")
	res, err = http.DefaultClient.Do(req)
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
	if strings.Contains(gotReferer, grant) || !strings.Contains(gotReferer, "tab=2") {
		t.Fatalf("guest Referer %q", gotReferer)
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestPreviewHTMLCommentControlFollowsUp(t *testing.T) {
	t.Parallel()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := "<!doctype html><html><body><p>app</p></body></html>"
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/gzip" {
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			_, _ = io.WriteString(gz, body)
			_ = gz.Close()
			return
		}
		_, _ = io.WriteString(w, body)
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

	res, err := http.Get(prev.URL + "/settings?g=" + grant)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("proxy %d %s", res.StatusCode, page)
	}
	html := string(page)
	if !strings.Contains(html, `id="rusui-preview-comment"`) {
		t.Fatal("missing comment control")
	}
	if !strings.Contains(html, `name="text"`) || !strings.Contains(html, `name="url"`) || !strings.Contains(html, `name="selector"`) {
		t.Fatalf("form fields %s", html)
	}
	if !strings.Contains(html, "/comment?g="+grant) {
		t.Fatalf("form action %s", html)
	}
	if !strings.Contains(html, `value="/settings"`) {
		t.Fatalf("url field %s", html)
	}
	if !strings.Contains(html, "<p>app</p>") {
		t.Fatalf("guest html lost %s", html)
	}

	gzRes, err := http.Get(prev.URL + "/gzip?g=" + grant)
	if err != nil {
		t.Fatal(err)
	}
	gzPage, _ := io.ReadAll(gzRes.Body)
	_ = gzRes.Body.Close()
	if gzRes.StatusCode != 200 || !strings.Contains(string(gzPage), `id="rusui-preview-comment"`) || !bytes.Contains(gzPage, []byte("<p>app</p>")) {
		t.Fatalf("gzip html %d %s", gzRes.StatusCode, gzPage)
	}

	req, err := http.NewRequest(http.MethodPost, prev.URL+"/comment?g="+grant, strings.NewReader(url.Values{
		"text": {"looks off"}, "url": {"/settings"}, "selector": {"p"},
	}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", prev.URL)
	posted, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = posted.Body.Close()
	if posted.StatusCode != http.StatusNoContent {
		t.Fatalf("comment %d", posted.StatusCode)
	}
	sess, err := store.GetSession(e.Store, sid)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sess.Prompt, "preview comment") || !strings.Contains(sess.Prompt, "looks off") || !strings.Contains(sess.Prompt, "url: /settings") {
		t.Fatalf("follow-up %q", sess.Prompt)
	}
}

func TestPreviewNonHTMLHasNoCommentControl(t *testing.T) {
	t.Parallel()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plain":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "hello")
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true}`)
		default:
			http.NotFound(w, r)
		}
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
	_, grant := mintPreviewGrant(t, s, hs, e, bport)

	for _, path := range []string{"/plain", "/json"} {
		res, err := http.Get(prev.URL + path + "?g=" + grant)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("%s %d %s", path, res.StatusCode, body)
		}
		if strings.Contains(string(body), "rusui-preview-comment") {
			t.Fatalf("%s injected: %s", path, body)
		}
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

func TestAPIMintPreviewGrant(t *testing.T) {
	t.Parallel()
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
	sid := readyPreviewSession(t, e, "api-preview")

	body, code := postMintPreview(t, hs, "op-tok", sid, port)
	if code != http.StatusOK {
		t.Fatalf("mint %d %s", code, body)
	}
	var got struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.URL, strings.TrimRight(prev.URL, "/")+"/?g=") {
		t.Fatalf("url %q", got.URL)
	}
	res, err := http.Get(got.URL)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || string(page) != "app-ok" {
		t.Fatalf("proxy %d %s", res.StatusCode, page)
	}

	grant := grantFrom(got.URL)
	res, err = http.Get(hs.URL + "/?g=" + grant)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("plane origin %d", res.StatusCode)
	}
}

func TestAPIMintPreviewErrorsMintNothing(t *testing.T) {
	t.Parallel()
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
	sid := readyPreviewSession(t, e, "api-preview-err")

	if body, code := postMintPreview(t, hs, "op-tok", 0, port); code != http.StatusNotFound {
		t.Fatalf("unknown session %d %s", code, body)
	}
	if body, code := postMintPreview(t, hs, "wsec", sid, port); code != http.StatusUnauthorized {
		t.Fatalf("worker %d %s", code, body)
	}
	if body, code := postMintPreview(t, hs, "op-tok", sid, 80); code != http.StatusBadRequest {
		t.Fatalf("port %d %s", code, body)
	}

	sess, _ := store.GetSession(e.Store, sid)
	envRow, _ := store.GetEnvironment(e.Store, sess.EnvironmentID)
	envRow.Handle = ""
	_ = store.UpdateEnvironment(e.Store, *envRow)
	if body, code := postMintPreview(t, hs, "op-tok", sid, port); code != http.StatusConflict || !strings.Contains(string(body), "environment not ready") {
		t.Fatalf("not ready %d %s", code, body)
	}

	envRow.Handle = filepath.Join(e.Env.(env.Process).Root, "sess")
	_ = store.UpdateEnvironment(e.Store, *envRow)
	s.PreviewBase = ""
	if body, code := postMintPreview(t, hs, "op-tok", sid, port); code != http.StatusConflict || !strings.Contains(string(body), "preview origin unset") {
		t.Fatalf("unset origin %d %s", code, body)
	}

	var n int
	if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM preview_grants WHERE session_id=?`, sid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("minted %d grants", n)
	}
}

func readyPreviewSession(t *testing.T, e *engine.Engine, prompt string) int64 {
	t.Helper()
	sid, err := e.StartRun("test", prompt, "")
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
	return sid
}

func TestAPIMintPreviewNamedService(t *testing.T) {
	t.Parallel()
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
	sid := readyPreviewSession(t, e, "named-preview")
	sess, _ := store.GetSession(e.Store, sid)
	envRow, _ := store.GetEnvironment(e.Store, sess.EnvironmentID)
	if err := os.MkdirAll(filepath.Join(envRow.Handle, ".rusui"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envRow.Handle, env.ServicesRusuiPath), []byte("services:\n  web:\n    command: true\n    port: "+strconv.Itoa(port)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	body, code := postMintPreviewJSON(t, hs, "op-tok", sid, `{"service":"web"}`)
	if code != http.StatusOK {
		t.Fatalf("named mint %d %s", code, body)
	}
	var n int
	var name string
	if err := e.Store.DB.QueryRow(`SELECT COUNT(*), COALESCE(MAX(service_name),'') FROM preview_grants WHERE session_id=?`, sid).Scan(&n, &name); err != nil || n != 1 || name != "web" {
		t.Fatalf("named grant n=%d name=%q err=%v", n, name, err)
	}

	if body, code := postMintPreviewJSON(t, hs, "op-tok", sid, `{"service":"nope"}`); code != http.StatusConflict || !strings.Contains(string(body), "not declared") {
		t.Fatalf("undeclared %d %s", code, body)
	}
	if err := os.WriteFile(filepath.Join(envRow.Handle, env.ServicesRusuiPath), []byte("services:\n  worker:\n    command: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if body, code := postMintPreviewJSON(t, hs, "op-tok", sid, `{"service":"worker"}`); code != http.StatusConflict || !strings.Contains(string(body), "no port") {
		t.Fatalf("portless %d %s", code, body)
	}
	missLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	miss := missLn.Addr().(*net.TCPAddr).Port
	_ = missLn.Close()
	if err := os.WriteFile(filepath.Join(envRow.Handle, env.ServicesRusuiPath), []byte("services:\n  web:\n    command: true\n    port: "+strconv.Itoa(miss)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if body, code := postMintPreviewJSON(t, hs, "op-tok", sid, `{"service":"web"}`); code != http.StatusConflict || !strings.Contains(string(body), "not healthy") {
		t.Fatalf("unhealthy %d %s", code, body)
	}
	if body, code := postMintPreviewJSON(t, hs, "op-tok", sid, `{"service":"web","port":`+strconv.Itoa(port)+`}`); code != http.StatusBadRequest {
		t.Fatalf("both %d %s", code, body)
	}
	if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM preview_grants WHERE session_id=?`, sid).Scan(&n); err != nil || n != 1 {
		t.Fatalf("refused mints still one grant: %d %v", n, err)
	}
}

func TestAPIMintPreviewRawPortHasNoServiceName(t *testing.T) {
	t.Parallel()
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
	sid := readyPreviewSession(t, e, "raw-preview")
	if body, code := postMintPreview(t, hs, "op-tok", sid, port); code != http.StatusOK {
		t.Fatalf("raw mint %d %s", code, body)
	}
	var name any
	if err := e.Store.DB.QueryRow(`SELECT service_name FROM preview_grants WHERE session_id=?`, sid).Scan(&name); err != nil || name != nil {
		t.Fatalf("raw service_name=%v err=%v", name, err)
	}
}

func postMintPreview(t *testing.T, hs *httptest.Server, token string, sid int64, port int) ([]byte, int) {
	t.Helper()
	return postMintPreviewJSON(t, hs, token, sid, `{"port":`+strconv.Itoa(port)+`}`)
}

func postMintPreviewJSON(t *testing.T, hs *httptest.Server, token string, sid int64, body string) ([]byte, int) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, hs.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/preview", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	return got, res.StatusCode
}

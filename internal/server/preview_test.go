package server

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

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
	ws := t.TempDir()
	envRow.Handle = ws
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

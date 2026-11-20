package server

import (
	"database/sql"
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
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

func enableShare(t *testing.T, e *engine.Engine) {
	t.Helper()
	p, err := policy.Parse([]byte(strings.Replace(slackPol, "  test:\n", "  test:\n    share: true\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	e.ReloadPolicy(p)
}

func declareWeb(t *testing.T, e *engine.Engine, sid int64, port int) {
	t.Helper()
	sess, err := store.GetSession(e.Store, sid)
	if err != nil {
		t.Fatal(err)
	}
	envRow, err := store.GetEnvironment(e.Store, sess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(envRow.Handle, ".rusui"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envRow.Handle, env.ServicesRusuiPath), []byte("services:\n  web:\n    command: true\n    port: "+strconv.Itoa(port)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func postMintShareJSON(t *testing.T, hs *httptest.Server, token string, sid int64, body string) ([]byte, int) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, hs.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/share", strings.NewReader(body))
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

func shareBackend(t *testing.T) int {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><html><body><p>app-ok</p></body></html>")
	}))
	t.Cleanup(backend.Close)
	_, bport, err := net.SplitHostPort(backend.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	p, _ := strconv.Atoi(bport)
	return p
}

func TestShareMintDisabledByDefaultAndOverlay(t *testing.T) {
	t.Parallel()
	port := shareBackend(t)
	s, hs, e := consoleEnv(t)
	prev := httptest.NewServer(s.PreviewHandler())
	t.Cleanup(prev.Close)
	s.PreviewBase = prev.URL
	sid := readyPreviewSession(t, e, "share-off")
	declareWeb(t, e, sid, port)

	body, code := postMintShareJSON(t, hs, "op-tok", sid, `{"service":"web"}`)
	if code != http.StatusConflict || !strings.Contains(string(body), "sharing disabled") {
		t.Fatalf("default %d %s", code, body)
	}
	if err := e.Store.Tx(func(tx *sql.Tx) error {
		return store.OverlaySet(tx, "share:test", "1")
	}); err != nil {
		t.Fatal(err)
	}
	body, code = postMintShareJSON(t, hs, "op-tok", sid, `{"service":"web"}`)
	if code != http.StatusConflict || !strings.Contains(string(body), "sharing disabled") {
		t.Fatalf("overlay %d %s", code, body)
	}
	var n int
	if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM preview_grants WHERE session_id=?`, sid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("minted %d err=%v", n, err)
	}
}

func TestShareMintOperatorNamedService(t *testing.T) {
	t.Parallel()
	port := shareBackend(t)
	s, hs, e := consoleEnv(t)
	prev := httptest.NewServer(s.PreviewHandler())
	t.Cleanup(prev.Close)
	s.PreviewBase = prev.URL
	enableShare(t, e)
	sid := readyPreviewSession(t, e, "share-mint")
	declareWeb(t, e, sid, port)

	body, code := postMintShareJSON(t, hs, "op-tok", sid, `{"service":"web","ttl":"4h"}`)
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
	grant := grantFrom(got.URL)
	if grant == "" {
		t.Fatal("no grant")
	}

	res, err := http.Get(got.URL)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(page), "app-ok") {
		t.Fatalf("proxy %d %s", res.StatusCode, page)
	}
	if res.Header.Get("X-Robots-Tag") != "noindex" || res.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("headers robots=%q referrer=%q", res.Header.Get("X-Robots-Tag"), res.Header.Get("Referrer-Policy"))
	}
	if strings.Contains(string(page), "rusui-preview-comment") {
		t.Fatalf("share html injected comment chrome: %s", page)
	}

	shares := sessionActionsOfType(t, hs, sid, actionPreviewShare)
	if len(shares) != 1 {
		t.Fatalf("share rows %d: %+v", len(shares), shares)
	}
	if strings.Contains(shares[0].Body, grant) {
		t.Fatalf("grant token in receipt %s", shares[0].Body)
	}
	var payload struct {
		Service string `json:"service"`
		Class   string `json:"class"`
		Port    int    `json:"port"`
	}
	if err := json.Unmarshal([]byte(shares[0].Body), &payload); err != nil || payload.Service != "web" || payload.Class != store.PreviewClassShare || payload.Port != port {
		t.Fatalf("receipt %+v err=%v", payload, err)
	}
}

func TestShareMintRefusesGuestWorkerTurnAndRawPort(t *testing.T) {
	t.Parallel()
	port := shareBackend(t)
	s, hs, e := consoleEnv(t)
	prev := httptest.NewServer(s.PreviewHandler())
	t.Cleanup(prev.Close)
	s.PreviewBase = prev.URL
	enableShare(t, e)
	sid := readyPreviewSession(t, e, "share-auth")
	declareWeb(t, e, sid, port)

	var turnID int64
	if err := e.Store.DB.QueryRow(`SELECT id FROM turns WHERE session_id=?`, sid).Scan(&turnID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Store.DB.Exec(`UPDATE turns SET state='leased', lease_generation=1 WHERE id=?`, turnID); err != nil {
		t.Fatal(err)
	}
	sec, err := issueTurnSecrets(e.Store, turnID, 1, e.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}

	if body, code := postMintShareJSON(t, hs, "wsec", sid, `{"service":"web"}`); code != http.StatusUnauthorized {
		t.Fatalf("worker %d %s", code, body)
	}
	if body, code := postMintShareJSON(t, hs, sec.Guest, sid, `{"service":"web"}`); code != http.StatusUnauthorized {
		t.Fatalf("guest %d %s", code, body)
	}
	if body, code := postMintShareJSON(t, hs, sec.Runner, sid, `{"service":"web"}`); code != http.StatusUnauthorized {
		t.Fatalf("turn %d %s", code, body)
	}
	if body, code := postMintShareJSON(t, hs, "op-tok", sid, `{"port":`+strconv.Itoa(port)+`}`); code != http.StatusBadRequest || !strings.Contains(string(body), "raw-port") {
		t.Fatalf("raw port %d %s", code, body)
	}
	if body, code := postMintShareJSON(t, hs, "op-tok", sid, `{"service":"web","ttl":"2h"}`); code != http.StatusBadRequest || !strings.Contains(string(body), "ttl") {
		t.Fatalf("ttl %d %s", code, body)
	}
	var n int
	if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM preview_grants WHERE session_id=?`, sid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("minted %d err=%v", n, err)
	}
}

func TestShareGrantReadOnlyCommentTerminalPlane(t *testing.T) {
	t.Parallel()
	port := shareBackend(t)
	s, hs, e := consoleEnv(t)
	prev := httptest.NewServer(s.PreviewHandler())
	t.Cleanup(prev.Close)
	s.PreviewBase = prev.URL
	enableShare(t, e)
	sid := readyPreviewSession(t, e, "share-ro")
	declareWeb(t, e, sid, port)
	body, code := postMintShareJSON(t, hs, "op-tok", sid, `{"service":"web"}`)
	if code != http.StatusOK {
		t.Fatalf("mint %d %s", code, body)
	}
	var got struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	grant := grantFrom(got.URL)

	req, err := http.NewRequest(http.MethodPost, prev.URL+"/comment?g="+grant, strings.NewReader(url.Values{"text": {"nope"}}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", prev.URL)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	msg, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden || !strings.Contains(string(msg), "read-only") {
		t.Fatalf("comment %d %s", res.StatusCode, msg)
	}
	if comments := sessionActionsOfType(t, hs, sid, actionPreviewComment); len(comments) != 0 {
		t.Fatalf("share comment inserted action %+v", comments)
	}

	term, err := http.NewRequest(http.MethodGet, hs.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/terminal", nil)
	if err != nil {
		t.Fatal(err)
	}
	term.Header.Set("Authorization", "Bearer "+grant)
	termRes, err := http.DefaultClient.Do(term)
	if err != nil {
		t.Fatal(err)
	}
	_ = termRes.Body.Close()
	if termRes.StatusCode != http.StatusUnauthorized {
		t.Fatalf("terminal %d", termRes.StatusCode)
	}

	plane, err := http.NewRequest(http.MethodGet, hs.URL+"/sessions/"+strconv.FormatInt(sid, 10), nil)
	if err != nil {
		t.Fatal(err)
	}
	plane.Header.Set("Authorization", "Bearer "+grant)
	planeRes, err := http.DefaultClient.Do(plane)
	if err != nil {
		t.Fatal(err)
	}
	_ = planeRes.Body.Close()
	if planeRes.StatusCode != http.StatusUnauthorized {
		t.Fatalf("plane %d", planeRes.StatusCode)
	}
}

func TestShareListRevoke(t *testing.T) {
	t.Parallel()
	port := shareBackend(t)
	s, hs, e := consoleEnv(t)
	prev := httptest.NewServer(s.PreviewHandler())
	t.Cleanup(prev.Close)
	s.PreviewBase = prev.URL
	enableShare(t, e)
	sid := readyPreviewSession(t, e, "share-list")
	declareWeb(t, e, sid, port)
	body, code := postMintShareJSON(t, hs, "op-tok", sid, `{"service":"web"}`)
	if code != http.StatusOK {
		t.Fatalf("mint %d %s", code, body)
	}
	var got struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	grant := grantFrom(got.URL)
	hash := store.HashPreviewToken(grant)

	listReq, err := http.NewRequest(http.MethodGet, hs.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/share", nil)
	if err != nil {
		t.Fatal(err)
	}
	listReq.Header.Set("Authorization", "Bearer op-tok")
	listRes, err := http.DefaultClient.Do(listReq)
	if err != nil {
		t.Fatal(err)
	}
	listBody, _ := io.ReadAll(listRes.Body)
	_ = listRes.Body.Close()
	if listRes.StatusCode != http.StatusOK {
		t.Fatalf("list %d %s", listRes.StatusCode, listBody)
	}
	var listed []map[string]any
	if err := json.Unmarshal(listBody, &listed); err != nil || len(listed) != 1 {
		t.Fatalf("list %s err=%v", listBody, err)
	}
	if listed[0]["token_hash"] != hash || listed[0]["service"] != "web" {
		t.Fatalf("listed %+v", listed[0])
	}
	if strings.Contains(string(listBody), grant) {
		t.Fatalf("secret in list %s", listBody)
	}

	delReq, err := http.NewRequest(http.MethodDelete, hs.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/share/"+hash, nil)
	if err != nil {
		t.Fatal(err)
	}
	delReq.Header.Set("Authorization", "Bearer op-tok")
	delRes, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = delRes.Body.Close()
	if delRes.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke %d", delRes.StatusCode)
	}

	res, err := http.Get(got.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("after revoke %d", res.StatusCode)
	}

	listRes, err = http.DefaultClient.Do(listReq)
	if err != nil {
		t.Fatal(err)
	}
	listBody, _ = io.ReadAll(listRes.Body)
	_ = listRes.Body.Close()
	if err := json.Unmarshal(listBody, &listed); err != nil || len(listed) != 0 {
		t.Fatalf("listed after revoke %s", listBody)
	}
}

func TestShareRotationEndsGrants(t *testing.T) {
	t.Parallel()
	port := shareBackend(t)
	s, hs, e := consoleEnv(t)
	prev := httptest.NewServer(s.PreviewHandler())
	t.Cleanup(prev.Close)
	s.PreviewBase = prev.URL
	enableShare(t, e)
	sid := readyPreviewSession(t, e, "share-rot")
	declareWeb(t, e, sid, port)
	body, code := postMintShareJSON(t, hs, "op-tok", sid, `{"service":"web"}`)
	if code != http.StatusOK {
		t.Fatalf("mint %d %s", code, body)
	}
	var got struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	res, err := http.Get(got.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("before rotate %d", res.StatusCode)
	}

	s.OperatorTok = "rotated-tok"
	if err := s.FenceOperatorToken(); err != nil {
		t.Fatal(err)
	}
	res, err = http.Get(got.URL)
	if err != nil {
		t.Fatal(err)
	}
	msg, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden || !strings.Contains(string(msg), "expired") {
		t.Fatalf("after rotate %d %s", res.StatusCode, msg)
	}
}

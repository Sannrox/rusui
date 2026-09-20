package server

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/engine"
)

func pkt(payload string) string {
	return fmt.Sprintf("%04x%s", 4+len(payload), payload)
}

func TestSplitReceivePackAndRefAllow(t *testing.T) {
	old := strings.Repeat("0", 40)
	nw := strings.Repeat("a", 40)
	body := pkt(old+" "+nw+" refs/heads/rusui/8/work\x00report-status") + "0000PACK"
	refs, rest, err := splitReceivePack(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0] != "refs/heads/rusui/8/work" {
		t.Fatalf("%q", refs)
	}
	b, _ := io.ReadAll(rest)
	if !strings.HasSuffix(string(b), "PACK") {
		t.Fatalf("rest %q", b)
	}
	if sessionRefAllowed(8, "refs/heads/main") {
		t.Fatal("main allowed")
	}
	if !sessionRefAllowed(8, "refs/heads/rusui/8/work") {
		t.Fatal("session ref denied")
	}
}

func TestGitProxyUploadPackSwapsAuth(t *testing.T) {
	var sawAuth, sawPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawPath = r.URL.Path
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(up.Close)
	origin, _ := url.Parse(up.URL)
	e, _, tok := leasedTurn(t)
	hs := httptest.NewServer((&Server{Eng: e, GitHubToken: "plane-pat", GitOrigin: origin}).Handler())
	t.Cleanup(hs.Close)
	req, _ := http.NewRequest("POST", hs.URL+"/git-proxy/github.com/example/test-repo.git/git-upload-pack", strings.NewReader("0000"))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("code %d", resp.StatusCode)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:plane-pat"))
	if sawAuth != want {
		t.Fatalf("auth %q", sawAuth)
	}
	if strings.Contains(sawAuth, tok) {
		t.Fatal("turn token leaked")
	}
	if sawPath != "/example/test-repo.git/git-upload-pack" {
		t.Fatalf("path %q", sawPath)
	}
}

func TestGitProxyForbidsOtherRepoAndMainPush(t *testing.T) {
	e, _, tok := leasedTurn(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream reached")
	}))
	t.Cleanup(up.Close)
	origin, _ := url.Parse(up.URL)
	hs := httptest.NewServer((&Server{Eng: e, GitHubToken: "plane-pat", GitOrigin: origin}).Handler())
	t.Cleanup(hs.Close)
	req, _ := http.NewRequest("POST", hs.URL+"/git-proxy/github.com/other/repo.git/git-upload-pack", strings.NewReader("x"))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("other repo %d", resp.StatusCode)
	}
	old := strings.Repeat("0", 40)
	nw := strings.Repeat("a", 40)
	body := pkt(old+" "+nw+" refs/heads/main\x00report-status") + "0000"
	req, _ = http.NewRequest("POST", hs.URL+"/git-proxy/github.com/example/test-repo.git/git-receive-pack", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("main push %d", resp.StatusCode)
	}
}

func TestGitProxyAllowsSessionRefPush(t *testing.T) {
	var sawPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(up.Close)
	origin, _ := url.Parse(up.URL)
	e, _, tok := leasedTurn(t)
	var sid int64
	if err := e.Store.DB.QueryRow(`SELECT id FROM sessions WHERE repo='example/test-repo' AND item=8`).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	old := strings.Repeat("0", 40)
	nw := strings.Repeat("a", 40)
	ref := fmt.Sprintf("refs/heads/rusui/%d/work", sid)
	body := pkt(old+" "+nw+" "+ref+"\x00report-status") + "0000"
	hs := httptest.NewServer((&Server{Eng: e, GitHubToken: "plane-pat", GitOrigin: origin}).Handler())
	t.Cleanup(hs.Close)
	req, _ := http.NewRequest("POST", hs.URL+"/git-proxy/github.com/example/test-repo.git/git-receive-pack", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("code %d", resp.StatusCode)
	}
	if sawPath != "/example/test-repo.git/git-receive-pack" {
		t.Fatalf("path %q", sawPath)
	}
}

func TestGitProxyRejectsUnapprovedHostExpiredCrossSessionAndPreparePush(t *testing.T) {
	e, clk, tok := leasedTurn(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream reached")
	}))
	t.Cleanup(up.Close)
	origin, _ := url.Parse(up.URL)
	hs := httptest.NewServer((&Server{Eng: e, GitHubToken: "plane-pat", GitOrigin: origin}).Handler())
	t.Cleanup(hs.Close)

	req, _ := http.NewRequest("POST", hs.URL+"/git-proxy/evil.example/x.git/git-upload-pack", strings.NewReader("0000"))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unapproved host %d", resp.StatusCode)
	}

	clk.Advance(engine.GrantTTL + time.Second)
	req, _ = http.NewRequest("POST", hs.URL+"/git-proxy/github.com/example/test-repo.git/git-upload-pack", strings.NewReader("0000"))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired %d", resp.StatusCode)
	}

	now := clk.T.UTC().Format(time.RFC3339Nano)
	res, err := e.Store.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1,'review','other/repo',1,'issue','open',?)`, now)
	if err != nil {
		t.Fatal(err)
	}
	sidB, _ := res.LastInsertId()
	if _, err := e.Store.DB.Exec(`INSERT INTO turns (session_id, lane, state) VALUES (?, 'review', 'leased')`, sidB); err != nil {
		t.Fatal(err)
	}
	var turnB int64
	if err := e.Store.DB.QueryRow(`SELECT id FROM turns WHERE session_id=?`, sidB).Scan(&turnB); err != nil {
		t.Fatal(err)
	}
	tokB, _, err := issueTurnToken(e.Store, turnB, 1, clk.T)
	if err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest("POST", hs.URL+"/git-proxy/github.com/example/test-repo.git/git-upload-pack", strings.NewReader("0000"))
	req.Header.Set("Authorization", "Bearer "+tokB)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-session %d", resp.StatusCode)
	}

	e3, _, _ := leasedTurn(t)
	prep, _, err := e3.IssuePrepareGrant("example/test-repo")
	if err != nil {
		t.Fatal(err)
	}
	hs3 := httptest.NewServer((&Server{Eng: e3, GitHubToken: "plane-pat", GitOrigin: origin}).Handler())
	t.Cleanup(hs3.Close)
	old := strings.Repeat("0", 40)
	nw := strings.Repeat("a", 40)
	body := pkt(old+" "+nw+" refs/heads/rusui/1/work\x00report-status") + "0000"
	req, _ = http.NewRequest("POST", hs3.URL+"/git-proxy/github.com/example/test-repo.git/git-receive-pack", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+prep)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("prepare push %d", resp.StatusCode)
	}

	upOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(upOK.Close)
	originOK, _ := url.Parse(upOK.URL)
	hsOK := httptest.NewServer((&Server{Eng: e3, GitHubToken: "plane-pat", GitOrigin: originOK}).Handler())
	t.Cleanup(hsOK.Close)
	req, _ = http.NewRequest("POST", hsOK.URL+"/git-proxy/github.com/example/test-repo.git/git-upload-pack", strings.NewReader("0000"))
	req.Header.Set("Authorization", "Bearer "+prep)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("prepare fetch %d", resp.StatusCode)
	}
}

func TestGitProxyMissingToken(t *testing.T) {
	e, _, tok := leasedTurn(t)
	hs := httptest.NewServer((&Server{Eng: e}).Handler())
	t.Cleanup(hs.Close)
	req, _ := http.NewRequest("POST", hs.URL+"/git-proxy/github.com/example/test-repo.git/git-upload-pack", strings.NewReader("0000"))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("code %d", resp.StatusCode)
	}
}

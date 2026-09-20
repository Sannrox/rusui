package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/engine"
)

func TestGitFetcherFetchAgainstTLSGitProxy(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("git required on PATH")
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(up.Close)
	origin, err := url.Parse(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	e, _, _ := leasedTurn(t)
	prep, _, err := e.IssuePrepareGrant("example/test-repo")
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{Eng: e, GitHubToken: "plane-pat", GitOrigin: origin, GuestHTTPSOnly: true}
	var proxyHTTP atomic.Int32
	hs := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHTTP.Add(1)
		srv.Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(hs.Close)
	ca := filepath.Join(t.TempDir(), "plane-ca.crt")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: hs.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSL_CAINFO", distinctCA(t))
	t.Setenv("GIT_SSL_NO_VERIFY", "1")
	g := engine.GitFetcher{
		ProxyURL: hs.URL + "/git-proxy/github.com/",
		Grant:    prep,
		CAFile:   ca,
	}
	if !strings.HasPrefix(g.ProxyURL, "https://") {
		t.Fatalf("proxy %s", g.ProxyURL)
	}
	err = g.Fetch("example/test-repo", "HEAD", t.TempDir())
	if proxyHTTP.Load() == 0 {
		t.Fatalf("TLS git-proxy never reached: %v", err)
	}

	proxyHTTP.Store(0)
	bad := engine.GitFetcher{ProxyURL: hs.URL + "/git-proxy/github.com/", Grant: prep, CAFile: distinctCA(t)}
	badErr := bad.Fetch("example/test-repo", "HEAD", t.TempDir())
	if n := proxyHTTP.Load(); n != 0 {
		t.Fatalf("untrusted CA still reached git-proxy hits=%d err=%v", n, badErr)
	}

	httpsNoCA := engine.GitFetcher{ProxyURL: hs.URL + "/git-proxy/github.com/", Grant: prep}
	if err := httpsNoCA.Fetch("example/test-repo", "HEAD", t.TempDir()); err == nil || !strings.Contains(err.Error(), "plane CA required") {
		t.Fatalf("https without CA: %v", err)
	}
}

func distinctCA(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "not-plane"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "other.crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

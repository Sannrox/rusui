package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

func emptySecretServer(t *testing.T) *httptest.Server {
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
	e.ReloadPolicy(p)
	s := &Server{Eng: e}
	hs := httptest.NewServer(s.Handler())
	t.Cleanup(hs.Close)
	return hs
}

func TestEmptyWorkerSecretRejectsClaim(t *testing.T) {
	hs := emptySecretServer(t)
	req, err := http.NewRequest("POST", hs.URL+"/jobs/claim", strings.NewReader(`{"repo":"example/test-repo"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("claim without worker secret %d", res.StatusCode)
	}
}

func TestEmptyWebhookSecretRejectsUnsigned(t *testing.T) {
	hs := emptySecretServer(t)
	body := `{"repository":{"full_name":"example/test-repo"},"issue":{"number":1}}`
	req, err := http.NewRequest("POST", hs.URL+"/hooks/github", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("unsigned github hook %d", res.StatusCode)
	}
}

func TestEmptySlackSecretRejectsUnsigned(t *testing.T) {
	hs := emptySecretServer(t)
	req, err := http.NewRequest("POST", hs.URL+"/hooks/slack", strings.NewReader("user_id=U1&text=status"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("unsigned slack hook %d", res.StatusCode)
	}
}

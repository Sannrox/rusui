package server

import (
	"encoding/json"
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

func eventEnv(t *testing.T) (*engine.Engine, *clock.Fake, *httptest.Server) {
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
	hs := httptest.NewServer((&Server{Eng: e, WebhookSec: "whsec"}).Handler())
	t.Cleanup(hs.Close)
	return e, clk, hs
}

func postEvent(t *testing.T, url string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", url, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Rusui-Signature-256", gh.Sign("whsec", body))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestGenericEventStoreThen202(t *testing.T) {
	e, clk, hs := eventEnv(t)
	body := []byte(`{"delivery_id":"evt-1","source":"cron","repo":"example/test-repo","item":7,"item_kind":"issue"}`)
	res := postEvent(t, hs.URL+"/hooks/events", body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("code %d", res.StatusCode)
	}
	n, err := store.CountEvents(e.Store, "example/test-repo", 7)
	if err != nil || n != 1 {
		t.Fatalf("events %d %v", n, err)
	}
	if e.GitHub.(*gh.Fake).CallCount() != 0 {
		t.Fatalf("github fetch on ingest: %d", e.GitHub.(*gh.Fake).CallCount())
	}
	var queued int
	if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM refresh_requests WHERE repo=? AND item=?`, "example/test-repo", 7).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("refresh queued %d %v", queued, err)
	}

	res = postEvent(t, hs.URL+"/hooks/events", body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("replay %d", res.StatusCode)
	}
	n, err = store.CountEvents(e.Store, "example/test-repo", 7)
	if err != nil || n != 1 {
		t.Fatalf("replay events %d %v", n, err)
	}

	old := clk.T.Add(-25 * time.Hour).Format(time.RFC3339)
	stale, err := json.Marshal(map[string]any{
		"delivery_id": "evt-old", "source": "cron", "repo": "example/test-repo",
		"item": 7, "item_kind": "issue", "occurred_at": old,
	})
	if err != nil {
		t.Fatal(err)
	}
	res = postEvent(t, hs.URL+"/hooks/events", stale)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("stale %d", res.StatusCode)
	}
	n, err = store.CountEvents(e.Store, "example/test-repo", 7)
	if err != nil || n != 1 {
		t.Fatalf("stale inserted %d %v", n, err)
	}
}

func TestGenericEventRejectsUnsigned(t *testing.T) {
	_, _, hs := eventEnv(t)
	req, err := http.NewRequest("POST", hs.URL+"/hooks/events", strings.NewReader(`{"delivery_id":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned %d", res.StatusCode)
	}
}

func TestEmptyWebhookSecretRejectsGenericEvent(t *testing.T) {
	hs := emptySecretServer(t)
	req, err := http.NewRequest("POST", hs.URL+"/hooks/events", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("empty secret %d", res.StatusCode)
	}
}

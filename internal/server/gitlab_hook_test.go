package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/gl"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

const gitlabPol = `version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
  review: true
  comments: false
  close: false
  implement: false
  land: false
  max_reviews_per_repo_per_utc_day: 50
projects:
  test:
    repos:
      example/test-repo:
        visibility: public
    gitlab:
      group/project:
        project_id: 42
        visibility: public
`

func gitlabEnv(t *testing.T) (*engine.Engine, *clock.Fake, *httptest.Server, string, *gl.Fake) {
	t.Helper()
	st := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	p, err := policy.Parse([]byte(gitlabPol))
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock.Fake{T: time.Unix(1_700_000_000, 0).UTC()}
	e := engine.New(st, p, gh.NewFake(), clk)
	gf := gl.NewFake()
	e.GitLab = gf
	e.ReloadPolicy(p)
	secret, err := gl.NewSigningToken()
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer((&Server{Eng: e, WebhookSec: "whsec", GitLabWebhookSec: secret}).Handler())
	t.Cleanup(hs.Close)
	return e, clk, hs, secret, gf
}

func TestGitLabIssueWebhookPersistedBefore2xxAndIdempotent(t *testing.T) {
	t.Parallel()
	e, clk, hs, secret, gf := gitlabEnv(t)
	body := gitlabIssueBody(42, "group/project", 7, "open", false)
	id := "msg_dup"
	code := postGitLab(t, hs, secret, id, strconv.FormatInt(clk.T.Unix(), 10), body)
	if code != http.StatusOK {
		t.Fatalf("code %d", code)
	}
	repo := policy.GitLabSourceKey(42)
	var n int
	if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM deliveries WHERE delivery_id=?`, gl.DeliveryID(id)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("deliveries %d %v", n, err)
	}
	if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE source='gitlab' AND repo=? AND item=7`, repo).Scan(&n); err != nil || n != 1 {
		t.Fatalf("events %d %v", n, err)
	}
	if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM refresh_requests WHERE repo=? AND item=7`, repo).Scan(&n); err != nil || n != 1 {
		t.Fatalf("refresh %d %v", n, err)
	}
	if gf.CallCount() != 0 {
		t.Fatalf("gitlab fetch on ingest: %d", gf.CallCount())
	}
	code = postGitLab(t, hs, secret, id, strconv.FormatInt(clk.T.Unix(), 10), body)
	if code != http.StatusOK {
		t.Fatalf("replay %d", code)
	}
	if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM deliveries WHERE delivery_id=?`, gl.DeliveryID(id)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("replay deliveries %d %v", n, err)
	}
}

func TestGitLabIssueWebhookRefusesBadSignatureStaleUnboundConfidential(t *testing.T) {
	t.Parallel()
	e, clk, hs, secret, _ := gitlabEnv(t)
	ts := strconv.FormatInt(clk.T.Unix(), 10)
	bound := gitlabIssueBody(42, "group/project", 7, "open", false)

	req, err := http.NewRequest(http.MethodPost, hs.URL+"/hooks/gitlab", bytes.NewReader(bound))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("webhook-id", "msg_bad")
	req.Header.Set("webhook-timestamp", ts)
	req.Header.Set("webhook-signature", "v1,AAAA")
	req.Header.Set("X-Gitlab-Event", gl.IssueHook)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad sig %d", res.StatusCode)
	}

	stale := strconv.FormatInt(clk.T.Add(-gl.MaxTimestampAge-time.Minute).Unix(), 10)
	if code := postGitLab(t, hs, secret, "msg_stale", stale, bound); code != http.StatusUnauthorized {
		t.Fatalf("stale %d", code)
	}

	unbound := gitlabIssueBody(99, "other/project", 1, "open", false)
	if code := postGitLab(t, hs, secret, "msg_unbound", ts, unbound); code != http.StatusNotFound {
		t.Fatalf("unbound %d", code)
	}

	conf := gitlabIssueBody(42, "group/project", 8, "open", true)
	if code := postGitLab(t, hs, secret, "msg_conf", ts, conf); code != http.StatusBadRequest {
		t.Fatalf("confidential %d", code)
	}

	// Plaintext token is not a signature.
	req, err = http.NewRequest(http.MethodPost, hs.URL+"/hooks/gitlab", bytes.NewReader(bound))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Gitlab-Token", secret)
	req.Header.Set("X-Gitlab-Event", gl.IssueHook)
	req.Header.Set("webhook-id", "msg_plain")
	req.Header.Set("webhook-timestamp", ts)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("plaintext token %d", res.StatusCode)
	}

	var n int
	if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM deliveries`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("refused deliveries %d %v", n, err)
	}
}

func TestGitLabRefreshAdmitsReviewWithoutGuestCredential(t *testing.T) {
	t.Parallel()
	e, clk, hs, secret, gf := gitlabEnv(t)
	repo := policy.GitLabSourceKey(42)
	it := snapshot.Item{
		Repo: repo, Item: 7, ItemKind: "issue", State: "open",
		Title: "bug", Body: "repro", UpdatedAt: clk.T.Format(time.RFC3339),
		CreatedAt: clk.T.Format(time.RFC3339),
		Provider:  policy.ProviderGitLab, Origin: gl.Origin,
		ProjectID: 42, ProjectPath: "group/project",
	}
	gf.Put(it)
	body := gitlabIssueBody(42, "group/project", 7, "open", false)
	if code := postGitLab(t, hs, secret, "msg_admit", strconv.FormatInt(clk.T.Unix(), 10), body); code != http.StatusOK {
		t.Fatalf("hook %d", code)
	}
	ok, err := e.StepRefresh()
	if err != nil || !ok {
		t.Fatalf("refresh %v ok=%v", err, ok)
	}
	j, err := store.JobState(e.Store, repo, 7)
	if err != nil || j == nil || j.State != "queued" {
		t.Fatalf("job %+v %v", j, err)
	}
	var sid int64
	if err := e.Store.DB.QueryRow(`SELECT id FROM sessions WHERE kind='review' AND repo=? AND item=?`, repo, 7).Scan(&sid); err != nil || sid == 0 {
		t.Fatalf("session %d %v", sid, err)
	}
}

func postGitLab(t *testing.T, hs *httptest.Server, secret, id, ts string, body []byte) int {
	t.Helper()
	sig, err := gl.Sign(secret, id, ts, body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, hs.URL+"/hooks/gitlab", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("webhook-id", id)
	req.Header.Set("webhook-timestamp", ts)
	req.Header.Set("webhook-signature", sig)
	req.Header.Set("X-Gitlab-Event", gl.IssueHook)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	return res.StatusCode
}

func gitlabIssueBody(projectID int64, path string, iid int, action string, confidential bool) []byte {
	p := map[string]any{
		"object_kind": "issue",
		"event_type":  "issue",
		"project": map[string]any{
			"id": projectID, "path_with_namespace": path,
			"web_url": "https://gitlab.com/" + path,
		},
		"object_attributes": map[string]any{
			"iid": iid, "action": action, "confidential": confidential, "type": "Issue",
		},
	}
	b, _ := json.Marshal(p)
	return b
}

package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/slack"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

const slackPol = `version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
  review: true
  comments: true
  close: true
  implement: false
  land: false
  max_reviews_per_repo_per_utc_day: 50
projects:
  test:
    repos:
      example/test-repo:
        visibility: public
`

func slackEnv(t *testing.T) (*Server, *engine.Engine, *clock.Fake, *httptest.Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p, err := policy.Parse([]byte(slackPol))
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock.Fake{T: time.Unix(1_700_000_000, 0).UTC()}
	f := gh.NewFake()
	e := engine.New(st, p, f, clk)
	e.ReloadPolicy(p)
	s := &Server{Eng: e, SlackSec: "ssec", SlackUsers: slack.ParseUsers("U1"), PolicyPath: filepath.Join(t.TempDir(), "missing.yaml")}
	hs := httptest.NewServer(s.Handler())
	t.Cleanup(hs.Close)
	return s, e, clk, hs
}

func slackPost(t *testing.T, hs *httptest.Server, clk *clock.Fake, user, text string) *http.Response {
	t.Helper()
	form := "user_id=" + user + "&command=%2Frusui&text=" + strings.ReplaceAll(text, " ", "+")
	body := []byte(form)
	ts := strconv.FormatInt(clk.T.Unix(), 10)
	req, _ := http.NewRequest("POST", hs.URL+"/hooks/slack", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", slack.Sign("ssec", ts, body))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func readText(t *testing.T, res *http.Response) (int, string) {
	t.Helper()
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var p struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(b, &p)
	return res.StatusCode, p.Text
}

func TestSlackSignatureAndAllowlist(t *testing.T) {
	_, _, clk, hs := slackEnv(t)
	form := "user_id=U1&text=status"
	req, _ := http.NewRequest("POST", hs.URL+"/hooks/slack", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Request-Timestamp", strconv.FormatInt(clk.T.Unix(), 10))
	req.Header.Set("X-Slack-Signature", "v0=nope")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("bad sig %d", res.StatusCode)
	}
	res = slackPost(t, hs, clk, "U2", "status")
	code, _ := readText(t, res)
	if code != 403 {
		t.Fatalf("allowlist %d", code)
	}
}

func TestSlackCommands(t *testing.T) {
	s, e, clk, hs := slackEnv(t)
	f := e.GitHub.(*gh.Fake)
	it := snapshot.Item{Repo: "example/test-repo", Item: 1, ItemKind: "issue", State: "open", Title: "t", Body: "b", DefaultBranch: "main", MainSHA: "aaa"}
	f.Put(it)
	_ = e.CatchUpItem(it.Repo, it.Item, it.ItemKind)
	_, _ = e.StepRefresh()

	code, text := readText(t, slackPost(t, hs, clk, "U1", "pause example/test-repo"))
	if code != 200 || !strings.Contains(text, "paused") {
		t.Fatalf("%d %s", code, text)
	}
	c, err := e.Claim("example/test-repo")
	if err == nil && c != nil {
		t.Fatal("claimed while paused")
	}
	_, text = readText(t, slackPost(t, hs, clk, "U1", "resume example/test-repo"))
	if !strings.Contains(text, "resumed") {
		t.Fatal(text)
	}
	_, text = readText(t, slackPost(t, hs, clk, "U1", "status example/test-repo"))
	if !strings.Contains(text, "example/test-repo#1") {
		t.Fatal(text)
	}
	_, text = readText(t, slackPost(t, hs, clk, "U1", "sweep example/test-repo"))
	if !strings.Contains(text, "sweep queued") {
		t.Fatal(text)
	}
	_, text = readText(t, slackPost(t, hs, clk, "U1", "implement"))
	if !strings.Contains(text, "rejected") {
		t.Fatal(text)
	}
	_, text = readText(t, slackPost(t, hs, clk, "U1", "nonesuch"))
	if !strings.Contains(text, "unknown") {
		t.Fatal(text)
	}

	for range engine.RetryLimit {
		c, err := e.Claim("example/test-repo")
		if err != nil || c == nil {
			t.Fatal(err)
		}
		clk.Advance(4 * time.Minute)
	}
	_, _ = e.Claim("example/test-repo")
	_, text = readText(t, slackPost(t, hs, clk, "U1", "retry example/test-repo#1"))
	if !strings.Contains(text, "retried") {
		t.Fatalf("retry %s", text)
	}
	j, _ := store.JobState(e.Store, "example/test-repo", 1)
	if j.State != "queued" || j.RetryCount != 0 {
		t.Fatalf("%s %d", j.State, j.RetryCount)
	}
	_ = s
}

func TestSlackChallenge(t *testing.T) {
	_, _, _, hs := slackEnv(t)
	req, _ := http.NewRequest("POST", hs.URL+"/hooks/slack", strings.NewReader(`{"type":"url_verification","challenge":"xyz"}`))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(b) != "xyz" {
		t.Fatalf("%s", b)
	}
}

func TestSlackExceptionNotify(t *testing.T) {
	_, e, clk, _ := slackEnv(t)
	var got []string
	e.Notify = func(msg string) { got = append(got, msg) }
	f := e.GitHub.(*gh.Fake)
	it := snapshot.Item{Repo: "example/test-repo", Item: 2, ItemKind: "issue", State: "open", Title: "t", Body: "b", DefaultBranch: "main", MainSHA: "aaa"}
	f.Put(it)
	_ = e.CatchUpItem(it.Repo, it.Item, it.ItemKind)
	_, _ = e.StepRefresh()
	for range engine.RetryLimit {
		c, err := e.Claim("example/test-repo")
		if err != nil || c == nil {
			t.Fatal(err)
		}
		clk.Advance(4 * time.Minute)
	}
	_, _ = e.Claim("example/test-repo")
	found := false
	for _, m := range got {
		if strings.Contains(m, "retry_limit exhausted") {
			found = true
		}
	}
	if !found {
		t.Fatalf("notify %v", got)
	}
}

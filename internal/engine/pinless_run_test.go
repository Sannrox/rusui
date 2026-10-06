package engine_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

const pinlessPolicy = `version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [run]
  egress: trusted
  review: true
  comments: false
  close: false
  implement: false
  land: false
  max_reviews_per_repo_per_utc_day: 50
projects:
  empty:
    repos: {}
    session_kinds: [run]
`

func pinlessHarness(t *testing.T) *harn {
	t.Helper()
	h := setup(t)
	p, err := policy.Parse([]byte(pinlessPolicy))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(p)
	return h
}

func TestPinlessRunSessionReachesAClaimedTurn(t *testing.T) {
	h := pinlessHarness(t)
	rt := &env.FakeRuntime{DefaultFiles: map[string]bool{env.SetupPath: true}}
	h.e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	tree := &engine.MemoryTree{Files: map[string][]byte{"README": []byte("from-pin\n"), env.SetupPath: []byte("#!/bin/sh\n")}}
	h.e.Tree = tree

	id, err := h.e.StartRun("empty", "look around", "")
	if err != nil || id == 0 {
		t.Fatalf("start %d %v", id, err)
	}
	sess, err := store.GetSession(h.st, id)
	if err != nil {
		t.Fatal(err)
	}
	key := policy.ProjectKey("empty")
	if sess.Repo != key || sess.Kind != store.SessionKindRun {
		t.Fatalf("session %+v", sess)
	}
	if h.f.CallCount() != 0 {
		t.Fatalf("github fetch on pinless start: %d", h.f.CallCount())
	}

	c, err := h.e.Claim(key)
	if err != nil || c == nil {
		t.Fatalf("claim %v %v", c, err)
	}
	if c.Job.Lane != "run" || c.Job.Repo != key {
		t.Fatalf("job %+v", c.Job)
	}
	if c.Snapshot.GitPin() != "" || c.Snapshot.MainSHA != "" {
		t.Fatalf("pin %+v", c.Snapshot)
	}
	if tree.Calls != 0 {
		t.Fatalf("fetched a pin: %d", tree.Calls)
	}

	turn, err := store.GetTurn(h.st, c.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	sess, err = store.GetSession(h.st, turn.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.GetEnvironment(h.st, sess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	wantHash := engine.SourceHash("rusui-guest:test", "", nil)
	if got.Handle == "" || got.SourceHash != wantHash {
		t.Fatalf("env %+v want hash %s", got, wantHash)
	}
	if _, err := rt.ReadFile(got.Handle, "README"); err == nil {
		t.Fatal("workspace was filled from a pin")
	}
	for _, cmd := range rt.Execs {
		if strings.Contains(strings.Join(cmd, " "), env.SetupPath) {
			t.Fatalf("setup ran: %v", rt.Execs)
		}
	}
	if h.f.CallCount() != 0 {
		t.Fatalf("github fetch on pinless claim: %d", h.f.CallCount())
	}
}

func TestPinlessRunRefusesReviewAndImplement(t *testing.T) {
	h := pinlessHarness(t)
	if _, err := h.e.StartRun("empty", "hi", ""); err != nil {
		t.Fatal(err)
	}
	_, err := h.e.RequestReview("example/test-repo", 1)
	if !errors.Is(err, engine.ErrReviewRepoUnbound) {
		t.Fatalf("review %v", err)
	}
	_, err = h.e.RequestReview(policy.ProjectKey("empty"), 1)
	if !errors.Is(err, engine.ErrReviewRepoUnbound) {
		t.Fatalf("project-key review %v", err)
	}
	_, err = h.e.StartTask("empty", engine.TaskSpec{
		EffortKey: "e1", Prompt: "p", Repo: "example/test-repo", Ref: "main", BaseSHA: "aaa",
		AllowedPaths: []string{"/pkg"},
	})
	if !errors.Is(err, engine.ErrTaskBlocked) {
		t.Fatalf("effort %v", err)
	}
}

func TestPinlessClaimHTTPOmitsGitProxy(t *testing.T) {
	h := pinlessHarness(t)
	rt := &env.FakeRuntime{}
	h.e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	if _, err := h.e.StartRun("empty", "hi", ""); err != nil {
		t.Fatal(err)
	}
	key := policy.ProjectKey("empty")
	req, _ := http.NewRequest("POST", h.http.URL+"/jobs/claim", strings.NewReader(`{"repo":"`+key+`"}`))
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["git_proxy_url"]; ok {
		t.Fatalf("git proxy on pinless claim: %s", b)
	}
	if _, ok := out["github_token"]; ok {
		t.Fatalf("github token on pinless claim: %s", b)
	}
	if out["repo"] != key {
		t.Fatalf("repo %v", out["repo"])
	}
}

func TestProjectKeyClaimRefusedWhenProjectHasRepos(t *testing.T) {
	h := setup(t)
	c, err := h.e.Claim(policy.ProjectKey("test"))
	if c != nil || err == nil {
		t.Fatalf("claim %+v %v", c, err)
	}
}

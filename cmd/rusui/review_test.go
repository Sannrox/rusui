package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/server"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

const reviewCLIPolicy = `version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
  review: true
  comments: true
  close: false
  implement: false
  land: false
  max_reviews_per_repo_per_utc_day: 2
projects:
  test:
    repos:
      example/test-repo:
        visibility: public
`

func TestReviewCLIBlackboxWaitsForCurrentPendingRevision(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	pol, err := policy.Parse([]byte(reviewCLIPolicy))
	if err != nil {
		t.Fatal(err)
	}
	fake := gh.NewFake()
	fake.Put(snapshot.Item{
		Repo: "example/test-repo", Item: 42, ItemKind: "pull", State: "open",
		Title: "review me", HeadSHA: "head-42", BaseSHA: "base-42", MainSHA: "main-42",
	})
	eng := engine.New(st, pol, fake, &clock.Fake{T: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)})
	eng.ReloadPolicy(pol)
	baseHandler := (&server.Server{Eng: eng, WorkerSec: "wsec", OperatorTok: "op"}).Handler()
	var sessionGets atomic.Int32
	var workerOnce sync.Once
	advanced := false
	freshCompleted := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sessions/") {
			sessionGets.Add(1)
			rec := httptest.NewRecorder()
			baseHandler.ServeHTTP(rec, r)
			var current struct {
				Turns        []store.Turn `json:"turns"`
				ReviewResult *struct {
					Artifact json.RawMessage `json:"artifact"`
				} `json:"review_result"`
			}
			if json.Unmarshal(rec.Body.Bytes(), &current) == nil && len(current.Turns) > 0 && current.ReviewResult != nil {
				var artifact struct {
					ClaimedRevision int `json:"claimed_revision"`
				}
				if json.Unmarshal(current.ReviewResult.Artifact, &artifact) == nil && artifact.ClaimedRevision == 1 && !advanced {
					fake.Put(snapshot.Item{
						Repo: "example/test-repo", Item: 42, ItemKind: "pull", State: "open",
						Title: "review me", HeadSHA: "head-43", BaseSHA: "base-42", MainSHA: "main-43",
					})
					latest, refreshErr := eng.RequestReview("example/test-repo", 42)
					if refreshErr != nil || latest.PendingRevision != 2 {
						http.Error(w, "test setup: newer review revision was not admitted", http.StatusInternalServerError)
						return
					}
					advanced = true
					rec = httptest.NewRecorder()
					baseHandler.ServeHTTP(rec, r)
				} else if advanced && !freshCompleted && artifact.ClaimedRevision == 1 && current.Turns[0].PendingRevision == 2 {
					claim, claimErr := eng.Claim("example/test-repo")
					if claimErr != nil || claim == nil || claim.Job.ClaimedRevision != 2 {
						http.Error(w, "test setup: newer review revision was not claimed", http.StatusInternalServerError)
						return
					}
					freshArtifact := engine.Artifact{
						SchemaVersion: 1, Repo: claim.Job.Repo, Item: claim.Job.Item,
						ItemKind: claim.Job.ItemKind, ClaimedRevision: claim.Job.ClaimedRevision,
						SnapshotHash: claim.ItemHash, MainSHA: claim.Snapshot.MainSHA,
						HeadSHA: claim.Snapshot.HeadSHA, Verdict: "propose_comment", Confidence: "high",
						ProposedActions: []engine.ProposedAction{{Type: "comment", ReasonCode: "operator_review"}},
					}
					if _, completeErr := eng.Complete(claim.Job.ID, claim.Job.LeaseGeneration, claim.Job.ClaimedRevision, freshArtifact); completeErr != nil {
						http.Error(w, "test setup: newer review revision did not complete", http.StatusInternalServerError)
						return
					}
					freshCompleted = true
					rec = httptest.NewRecorder()
					baseHandler.ServeHTTP(rec, r)
				}
			}
			for key, values := range rec.Header() {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}
			w.WriteHeader(rec.Code)
			_, _ = w.Write(rec.Body.Bytes())
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/reviews" {
			baseHandler.ServeHTTP(w, r)
			return
		}
		rec := httptest.NewRecorder()
		baseHandler.ServeHTTP(rec, r)
		if rec.Code < 300 {
			workerOnce.Do(func() {
				go func() {
					time.Sleep(100 * time.Millisecond)
					claim, claimErr := eng.Claim("example/test-repo")
					if claimErr != nil || claim == nil {
						t.Errorf("worker claim: claim=%v err=%v", claim, claimErr)
						return
					}
					artifact := engine.Artifact{
						SchemaVersion: 1, Repo: claim.Job.Repo, Item: claim.Job.Item,
						ItemKind: claim.Job.ItemKind, ClaimedRevision: claim.Job.ClaimedRevision,
						SnapshotHash: claim.ItemHash, MainSHA: claim.Snapshot.MainSHA,
						HeadSHA: claim.Snapshot.HeadSHA, Verdict: "propose_comment", Confidence: "high",
						ProposedActions: []engine.ProposedAction{{Type: "comment", ReasonCode: "operator_review"}},
					}
					if _, completeErr := eng.Complete(claim.Job.ID, claim.Job.LeaseGeneration, claim.Job.ClaimedRevision, artifact); completeErr != nil {
						t.Errorf("worker completion: %v", completeErr)
					}
				}()
			})
		}
		for key, values := range rec.Header() {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	})
	hs := httptest.NewServer(handler)
	t.Cleanup(hs.Close)

	bin := filepath.Join(t.TempDir(), "rusui")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	cmd := exec.Command(bin, "review", "-url", hs.URL, "-token", "wsec", "-timeout", "3s", "-poll-interval", "5ms", "example/test-repo#42")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("review CLI: %v\n%s", err, output)
	}
	got := string(output)
	for _, want := range []string{"Review session", "advanced; waiting for revision 2", `"revision_id": 2`, `"claimed_revision": 2`, `"head_sha": "head-43"`, `"dry_run_actions"`, "dry-run comment operator_review"} {
		if !strings.Contains(got, want) {
			t.Fatalf("CLI output missing %q:\n%s", want, got)
		}
	}
	if sessionGets.Load() < 2 {
		t.Fatalf("CLI did not wait and poll the session: GET count %d", sessionGets.Load())
	}

	var sessionID int64
	if err := eng.Store.DB.QueryRow(`SELECT id FROM sessions WHERE kind='review' AND repo='example/test-repo' AND item=42`).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}
	turns, err := store.ListTurnsForSession(eng.Store, sessionID)
	if err != nil || len(turns) != 1 {
		t.Fatalf("review turns: %v %v", turns, err)
	}
	n, err := store.CountReviews(eng.Store, turns[0].ID)
	if err != nil || n != 2 {
		t.Fatalf("review revisions %d: %v", n, err)
	}
	if _, _, ok, err := store.LatestReviewForSession(eng.Store, sessionID); err != nil || !ok {
		t.Fatalf("latest review: ok=%v err=%v", ok, err)
	}
	if fake.CallCount() != 2 {
		t.Fatalf("GitHub reads after current revision refresh %d", fake.CallCount())
	}

	req, err := http.NewRequest(http.MethodPost, hs.URL+"/reviews", bytes.NewReader([]byte(`{"repo":"example/test-repo","item":42}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	reply, readErr := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	var repeated reviewRequestResponse
	if res.StatusCode != http.StatusOK || json.Unmarshal(reply, &repeated) != nil {
		t.Fatalf("repeat review request: %d %s", res.StatusCode, reply)
	}
	if repeated.SessionID != sessionID || repeated.PendingRevision != 2 || repeated.TurnID != turns[0].ID {
		t.Fatalf("repeat returned a new session or revision: %+v", repeated)
	}
	if count, err := store.CountReviews(eng.Store, turns[0].ID); err != nil || count != 2 {
		t.Fatalf("repeat created another review revision: %d %v", count, err)
	}
	if fake.CallCount() != 3 {
		t.Fatalf("GitHub reads after idempotent repeat %d", fake.CallCount())
	}
}

func TestParseReviewTarget(t *testing.T) {
	for _, tc := range []struct {
		input string
		repo  string
		item  int
		bad   bool
	}{
		{input: "owner/repo#42", repo: "owner/repo", item: 42},
		{input: "owner/repo", bad: true},
		{input: "owner/repo#0", bad: true},
		{input: "owner/repo#1#2", bad: true},
		{input: "owner/repo/extra#1", bad: true},
	} {
		repo, item, err := parseReviewTarget(tc.input)
		if tc.bad {
			if err == nil {
				t.Fatalf("parseReviewTarget(%q) unexpectedly succeeded", tc.input)
			}
			continue
		}
		if err != nil || repo != tc.repo || item != tc.item {
			t.Fatalf("parseReviewTarget(%q) = %q %d %v", tc.input, repo, item, err)
		}
	}
}

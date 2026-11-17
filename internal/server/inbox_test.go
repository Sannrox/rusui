package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/store"
)

// inboxResult inserts a session of kind with one turn and the given result
// item hashes, and returns the session id and the last result id.
func inboxResult(t *testing.T, e *engine.Engine, kind string, item int, hashes ...string) (int64, int64) {
	t.Helper()
	var sid int64
	if err := e.Store.DB.QueryRow(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1, ?, 'example/test-repo', ?, 'issue', 'open', '2026-01-01T00:00:00Z') RETURNING id`, kind, item).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	var tid int64
	if err := e.Store.DB.QueryRow(`INSERT INTO turns (session_id, lane, state) VALUES (?, 'review', 'completed') RETURNING id`, sid).Scan(&tid); err != nil {
		t.Fatal(err)
	}
	var rid int64
	for _, h := range hashes {
		if err := e.Store.DB.QueryRow(`INSERT INTO review_revisions (job_id, claimed_revision, item_hash, main_sha, payload) VALUES (?, 1, ?, 'm', '{"verdict":"ok","proposed_actions":[{},{}]}') RETURNING id`, tid, h).Scan(&rid); err != nil {
			t.Fatal(err)
		}
	}
	return sid, rid
}

func getInbox(t *testing.T, url, tok string) (int, inboxResponse) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var out inboxResponse
	if res.StatusCode == http.StatusOK {
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
	}
	return res.StatusCode, out
}

func TestInboxListsLatestUndisposedCurrentReviewResults(t *testing.T) {
	t.Parallel()
	_, hs, e := consoleEnv(t)
	snapshot := func(item int, hash string) {
		t.Helper()
		if _, err := e.Store.DB.Exec(`INSERT INTO snapshots (repo, item, revision, item_kind, item_hash, main_sha, payload) VALUES ('example/test-repo', ?, 1, 'issue', ?, 'm', '{}')`, item, hash); err != nil {
			t.Fatal(err)
		}
	}
	_, open := inboxResult(t, e, "review", 1, "h1", "h1") // reviewed twice, unchanged
	snapshot(1, "h1")
	_, stale := inboxResult(t, e, "review", 2, "old")
	snapshot(2, "new")
	_, done := inboxResult(t, e, "review", 3, "h3")
	if _, err := store.RecordDisposition(e.Store, store.Disposition{ReviewRevisionID: done, Value: store.DispositionUseful}); err != nil {
		t.Fatal(err)
	}
	foreign, _ := inboxResult(t, e, "review", 4, "h4")
	if err := store.SetSessionTenant(e.Store, foreign, "globex"); err != nil {
		t.Fatal(err)
	}
	inboxResult(t, e, "run", 5, "h5")
	if _, err := e.Store.DB.Exec(`INSERT INTO daily_review_counts (repo, day, count) VALUES ('example/test-repo', ?, 50)`,
		e.Clock.Now().UTC().Format("2006-01-02")); err != nil {
		t.Fatal(err)
	}

	code, got := getInbox(t, hs.URL+"/inbox", "op-tok")
	if code != http.StatusOK || len(got.Items) != 1 {
		t.Fatalf("default view %d %+v", code, got.Items)
	}
	if it := got.Items[0]; it.ResultID != open || it.State != store.InboxCurrent || it.Next != store.InboxNextDispose || it.Verdict != "ok" || it.ProposedActions != 2 {
		t.Fatalf("open item %+v", it)
	}
	want := inboxRepo{Repo: "example/test-repo", ReviewsToday: 50, ReviewCap: 50, BudgetExhausted: true, Actionable: 1}
	if len(got.Repos) != 1 || got.Repos[0] != want {
		t.Fatalf("repos %+v", got.Repos)
	}

	_, all := getInbox(t, hs.URL+"/inbox?all=1", "op-tok")
	byResult := map[int64]store.InboxItem{}
	for _, it := range all.Items {
		byResult[it.ResultID] = it
	}
	if len(all.Items) != 3 {
		t.Fatalf("all view lists other tenants or kinds: %+v", all.Items)
	}
	if it := byResult[stale]; it.State != store.InboxSuperseded || it.Next != "" {
		t.Fatalf("stale item %+v", it)
	}
	if it := byResult[done]; it.Disposition == nil || it.Disposition.Value != store.DispositionUseful || it.Next != "" {
		t.Fatalf("disposed item %+v", it)
	}
}

func TestInboxRefusesWorkerTokenAndBadFilter(t *testing.T) {
	t.Parallel()
	_, hs, _ := consoleEnv(t)
	if code, _ := getInbox(t, hs.URL+"/inbox", "wsec"); code != http.StatusUnauthorized {
		t.Fatalf("worker token %d", code)
	}
	if code, _ := getInbox(t, hs.URL+"/inbox?all=yes", "op-tok"); code != http.StatusBadRequest {
		t.Fatalf("bad filter %d", code)
	}
}

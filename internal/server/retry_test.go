package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/snapshot"
)

// `rusui retry` requeues a failed review job without Slack; worker tokens
// and unknown items are refused (#401).
func TestRetryRequeuesFailedReviewForOperator(t *testing.T) {
	_, hs, e := consoleEnv(t)
	e.GitHub.(*gh.Fake).Put(snapshot.Item{Repo: "example/test-repo", Item: 5, ItemKind: "issue", State: "open", Title: "t", DefaultBranch: "main", MainSHA: "aaa"})
	if err := e.CatchUpItem("example/test-repo", 5, "issue"); err != nil {
		t.Fatal(err)
	}
	for {
		ok, err := e.StepRefresh()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
	}
	if _, err := e.Store.DB.Exec(`UPDATE turns SET state='failed' WHERE id=(SELECT id FROM jobs WHERE item=5 AND lane='review')`); err != nil {
		t.Fatal(err)
	}
	post := func(tok, target string) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, hs.URL+"/retry", strings.NewReader(`{"target":"`+target+`"}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	if code := post("wsec", "example/test-repo#5"); code != http.StatusUnauthorized {
		t.Fatalf("worker token %d", code)
	}
	if code := post("op-tok", "example/test-repo#99"); code != http.StatusConflict {
		t.Fatalf("unknown item %d", code)
	}
	req, err := http.NewRequest(http.MethodPost, hs.URL+"/retry", strings.NewReader(`{"target":"example/test-repo#5"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer op-tok")
	req.Header.Set("X-Member-Id", "alice")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("retry %d", res.StatusCode)
	}
	var state, actor string
	if err := e.Store.DB.QueryRow(`SELECT state FROM jobs WHERE item=5 AND lane='review'`).Scan(&state); err != nil || state != "queued" {
		t.Fatalf("state %q %v", state, err)
	}
	if err := e.Store.DB.QueryRow(`SELECT actor FROM retry_audit`).Scan(&actor); err != nil || actor != "operator-api" {
		t.Fatalf("audit %q %v", actor, err)
	}
}

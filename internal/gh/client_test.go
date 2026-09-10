package gh

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseHookIDs(t *testing.T) {
	m := ParseHookIDs("example/test-repo=42, other/r=9")
	if m["example/test-repo"] != "42" || m["other/r"] != "9" {
		t.Fatalf("%v", m)
	}
}

func TestAPIGetItemIssueAndPR(t *testing.T) {
	var sawAuth bool
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/example/test-repo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "auth", 401)
			return
		}
		sawAuth = true
		json.NewEncoder(w).Encode(map[string]string{"default_branch": "main", "full_name": "example/test-repo"})
	})
	mux.HandleFunc("/repos/example/test-repo/git/ref/heads/main", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": "aaa111"}})
	})
	mux.HandleFunc("/repos/example/test-repo/issues/1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"number": 1, "title": "bug", "body": "see #2 and #2", "state": "open",
			"created_at": "2025-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z",
			"labels": []map[string]string{{"name": "bug"}},
		})
	})
	mux.HandleFunc("/repos/example/test-repo/issues/1/comments", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"body": "bot", "created_at": "2025-02-01T00:00:00Z", "user": map[string]string{"login": "x[bot]", "type": "Bot"}},
			{"body": "human", "created_at": "2025-03-01T00:00:00Z", "user": map[string]string{"login": "alice", "type": "User"}},
		})
	})
	mux.HandleFunc("/repos/example/test-repo/issues/7", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"number": 7, "title": "pr", "body": "fix", "state": "closed",
			"created_at": "2025-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z",
			"pull_request": map[string]string{"url": "https://api.github.com/repos/example/test-repo/pulls/7"},
		})
	})
	mux.HandleFunc("/repos/example/test-repo/issues/7/comments", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]any{})
	})
	mux.HandleFunc("/repos/example/test-repo/pulls/7", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"number": 7, "title": "pr", "body": "fix", "state": "closed", "draft": false,
			"merged": true, "merge_commit_sha": "abc999",
			"created_at": "2025-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z",
			"base": map[string]string{"ref": "main", "sha": "base1"},
			"head": map[string]string{"sha": "head1"},
		})
	})
	mux.HandleFunc("/repos/example/test-repo/issues/404", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing", 404)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	api := NewAPI("tok", nil)
	api.BaseURL = srv.URL

	it, err := api.GetItem("example/test-repo", 1, "issue")
	if err != nil {
		t.Fatal(err)
	}
	if !sawAuth {
		t.Fatal("missing bearer")
	}
	if it.MainSHA != "aaa111" || it.Title != "bug" || it.NonBotCommentCount != 1 {
		t.Fatalf("%+v", it)
	}
	if it.LastNonBotCommentAt == "" {
		t.Fatal("expected last human comment")
	}
	if len(it.LinkedSameRepoItems) != 1 || it.LinkedSameRepoItems[0] != 2 {
		t.Fatalf("linked %v", it.LinkedSameRepoItems)
	}

	pr, err := api.GetItem("example/test-repo", 7, "issue")
	if err != nil {
		t.Fatal(err)
	}
	if pr.ItemKind != "pull" || !pr.MergedIntoDefault || pr.HeadSHA != "head1" || pr.MergeCommitSHA != "abc999" {
		t.Fatalf("%+v", pr)
	}

	ok, err := api.GetItemExists("example/test-repo", 1)
	if err != nil || !ok {
		t.Fatalf("exists %v %v", ok, err)
	}
	ok, err = api.GetItemExists("example/test-repo", 404)
	if err != nil || ok {
		t.Fatalf("404 exists %v %v", ok, err)
	}
}

func TestAPIIsAncestor(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/example/test-repo/compare/", func(w http.ResponseWriter, r *http.Request) {
		spec := strings.TrimPrefix(r.URL.Path, "/repos/example/test-repo/compare/")
		switch spec {
		case "old...aaa111":
			json.NewEncoder(w).Encode(map[string]string{"status": "ahead"})
		case "aaa111...aaa111":
			json.NewEncoder(w).Encode(map[string]string{"status": "identical"})
		case "other...aaa111":
			json.NewEncoder(w).Encode(map[string]string{"status": "diverged"})
		default:
			http.Error(w, "no", 404)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	api := NewAPI("tok", nil)
	api.BaseURL = srv.URL

	ok, err := api.IsAncestor("example/test-repo", "old", "aaa111")
	if err != nil || !ok {
		t.Fatalf("ahead %v %v", ok, err)
	}
	ok, err = api.IsAncestor("example/test-repo", "aaa111", "aaa111")
	if err != nil || !ok {
		t.Fatalf("identical %v %v", ok, err)
	}
	ok, err = api.IsAncestor("example/test-repo", "other", "aaa111")
	if err != nil || ok {
		t.Fatalf("diverged %v %v", ok, err)
	}
	ok, err = api.IsAncestor("example/test-repo", "missing", "aaa111")
	if err != nil || ok {
		t.Fatalf("404 %v %v", ok, err)
	}
}

func TestAPIDeliveries(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/example/test-repo/hooks/99/deliveries/55", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"id": 55, "delivered_at": "2026-09-10T00:00:00Z", "event": "issues",
			"payload": map[string]any{"repository": map[string]string{"full_name": "example/test-repo"}, "issue": map[string]int{"number": 1}},
		})
	})
	mux.HandleFunc("/repos/example/test-repo/hooks/99/deliveries", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 55, "delivered_at": "2026-09-10T00:00:00Z", "status": "OK"},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	api := NewAPI("tok", ParseHookIDs("example/test-repo=99"))
	api.BaseURL = srv.URL

	list, err := api.ListDeliveries("example/test-repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "55" {
		t.Fatalf("%v", list)
	}
	d, err := api.GetDelivery("example/test-repo", "55")
	if err != nil {
		t.Fatal(err)
	}
	if d.Event != "issues" || d.ID != "55" {
		t.Fatalf("%+v", d)
	}
	repo, item, kind, err := ParseWebhook(d.Payload)
	if err != nil || repo != "example/test-repo" || item != 1 || kind != "issue" {
		t.Fatalf("%s %d %s %v", repo, item, kind, err)
	}
	if _, err := api.ListDeliveries("no/hook"); err == nil {
		t.Fatal("expected hook id error")
	}
}

func TestAPIErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		io.WriteString(w, `{"message":"rate limited"}`)
	}))
	t.Cleanup(srv.Close)
	api := NewAPI("tok", nil)
	api.BaseURL = srv.URL
	_, err := api.GetItem("example/test-repo", 1, "issue")
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("%v", err)
	}
}

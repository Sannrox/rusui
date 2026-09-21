package gh

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIOpenDraftPRAndFind(t *testing.T) {
	var posts int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/example/test-repo/pulls":
			posts++
			if r.Header.Get("Authorization") != "Bearer ghs_install" {
				t.Fatalf("auth %q", r.Header.Get("Authorization"))
			}
			b, _ := io.ReadAll(r.Body)
			var payload map[string]any
			if err := json.Unmarshal(b, &payload); err != nil {
				t.Fatal(err)
			}
			if payload["draft"] != true || payload["head"] != "rusui/7/work" || payload["base"] != "main" {
				t.Fatalf("payload %+v", payload)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number":   9,
				"title":    payload["title"],
				"body":     payload["body"],
				"html_url": "https://github.com/example/test-repo/pull/9",
				"draft":    true,
				"head":     map[string]string{"ref": "rusui/7/work", "sha": "abc123"},
				"base":     map[string]string{"ref": "main"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/example/test-repo/pulls":
			if r.URL.Query().Get("head") != "example:rusui/7/work" {
				t.Fatalf("head %q", r.URL.Query().Get("head"))
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"number":   9,
				"title":    "t",
				"body":     "b",
				"html_url": "https://github.com/example/test-repo/pull/9",
				"draft":    true,
				"head":     map[string]string{"ref": "rusui/7/work", "sha": "abc123"},
				"base":     map[string]string{"ref": "main"},
			}})
		default:
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(ts.Close)
	api := NewAPI("ghs_install", nil)
	api.BaseURL = ts.URL
	api.HTTP = ts.Client()
	pr, err := api.OpenDraftPR("example/test-repo", "t", "b", "refs/heads/rusui/7/work", "main")
	if err != nil || pr == nil || pr.Number != 9 || pr.HeadSHA != "abc123" || !pr.Draft {
		t.Fatalf("%+v %v", pr, err)
	}
	found, err := api.FindDraftPR("example/test-repo", "refs/heads/rusui/7/work")
	if err != nil || found == nil || found.Number != 9 {
		t.Fatalf("%+v %v", found, err)
	}
	if posts != 1 {
		t.Fatalf("posts %d", posts)
	}
}

func TestAPIDefaultBranch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/example/test-repo" {
			t.Fatalf("%s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"default_branch": "trunk"})
	}))
	t.Cleanup(ts.Close)
	api := NewAPI("t", nil)
	api.BaseURL = ts.URL
	api.HTTP = ts.Client()
	b, err := api.DefaultBranch("example/test-repo")
	if err != nil || b != "trunk" {
		t.Fatalf("%q %v", b, err)
	}
}

func TestAPIFindDraftPREmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{})
	}))
	t.Cleanup(ts.Close)
	api := NewAPI("t", nil)
	api.BaseURL = ts.URL
	api.HTTP = ts.Client()
	pr, err := api.FindDraftPR("example/test-repo", "rusui/7/work")
	if err != nil || pr != nil {
		t.Fatalf("%+v %v", pr, err)
	}
}

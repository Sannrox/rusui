package gl

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/policy"
)

func TestSignVerifyAndRefuseStale(t *testing.T) {
	t.Parallel()
	secret, err := NewSigningToken()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"object_kind":"issue"}`)
	id := "msg_1"
	ts := "1700000000"
	sig, err := Sign(secret, id, ts, body)
	if err != nil {
		t.Fatal(err)
	}
	if !Verify(secret, id, ts, sig, body) {
		t.Fatal("valid signature rejected")
	}
	if Verify(secret, id, ts, sig, append(body, 'x')) {
		t.Fatal("tampered body accepted")
	}
	if Verify(secret, "other", ts, sig, body) {
		t.Fatal("wrong id accepted")
	}
	now := time.Unix(1700000000, 0).UTC()
	if !TimestampFresh(ts, now, MaxTimestampAge) {
		t.Fatal("current timestamp stale")
	}
	if TimestampFresh(ts, now.Add(MaxTimestampAge+time.Second), MaxTimestampAge) {
		t.Fatal("stale timestamp accepted")
	}
	if Verify("", id, ts, sig, body) || Verify(secret, id, ts, "", body) {
		t.Fatal("empty secret or signature accepted")
	}
}

func TestParseIssueWebhookRefusesConfidentialAndOtherKinds(t *testing.T) {
	t.Parallel()
	body := issueHookBody(1, "group/project", 23, "open", false)
	ev, err := ParseIssueWebhook(IssueHook, body)
	if err != nil || ev.ProjectID != 1 || ev.IID != 23 || ev.Action != "open" {
		t.Fatalf("%+v %v", ev, err)
	}
	if _, err := ParseIssueWebhook("Merge Request Hook", body); err == nil {
		t.Fatal("merge request accepted")
	}
	conf := issueHookBody(1, "group/project", 23, "open", true)
	if _, err := ParseIssueWebhook(IssueHook, conf); !errors.Is(err, ErrConfidential) {
		t.Fatalf("confidential: %v", err)
	}
	if _, err := ParseIssueWebhook("Confidential Issue Hook", body); err == nil {
		t.Fatal("confidential hook accepted")
	}
}

func TestAPIGetIssueMapsOpenedAndRefusesConfidential(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v4/projects/9/issues/4", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "glpat-test" {
			http.Error(w, "auth", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"iid": 4, "title": "bug", "description": "repro",
			"state": "opened", "confidential": false, "labels": []string{"bug"},
			"updated_at": "2026-01-02T00:00:00Z", "created_at": "2026-01-01T00:00:00Z",
		})
	})
	mux.HandleFunc("GET /api/v4/projects/9/issues/5", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"iid": 5, "title": "secret", "state": "opened", "confidential": true,
		})
	})
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)
	api := NewAPI("glpat-test")
	api.BaseURL = hs.URL + "/api/v4"
	it, err := api.GetIssue(9, "group/project", 4)
	if err != nil {
		t.Fatal(err)
	}
	if it.Repo != policy.GitLabSourceKey(9) || it.State != "open" || it.Provider != policy.ProviderGitLab {
		t.Fatalf("%+v", it)
	}
	if it.Origin != Origin || it.ProjectPath != "group/project" || it.Body != "repro" {
		t.Fatalf("%+v", it)
	}
	if _, err := api.GetIssue(9, "group/project", 5); !errors.Is(err, ErrConfidential) {
		t.Fatalf("confidential issue: %v", err)
	}
}

func TestDeliveryIDNamespacesProviderAndOrigin(t *testing.T) {
	t.Parallel()
	id := DeliveryID("msg_abc")
	if id != "gitlab:https://gitlab.com:msg_abc" {
		t.Fatalf("%s", id)
	}
	if strings.Contains(id, "X-Gitlab-Event-UUID") {
		t.Fatal("must not use event uuid")
	}
}

func issueHookBody(projectID int64, path string, iid int, action string, confidential bool) []byte {
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

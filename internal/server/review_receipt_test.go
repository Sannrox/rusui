package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/receipt"
	"github.com/sannrox/rusui/internal/store"
)

func TestSessionReceiptExportsStoredReview(t *testing.T) {
	t.Parallel()
	_, hs, e := consoleEnv(t)
	sid := seedSessionReview(t, e.Store, "example/test-repo", 7, "keep", "")
	req, err := http.NewRequest(http.MethodGet, hs.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/receipt", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer op-tok")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	var exp receipt.Export
	if err := json.Unmarshal(body, &exp); err != nil {
		t.Fatal(err)
	}
	if exp.SessionID != sid || exp.Verdict != "keep" || exp.RecoveryInput || exp.Signed {
		t.Fatalf("%+v", exp)
	}
	if exp.GitHubAction.Status != "none" {
		t.Fatalf("github %+v", exp.GitHubAction)
	}

	req, err = http.NewRequest(http.MethodGet, hs.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/receipt?format=md", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer op-tok")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	md, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || !strings.Contains(res.Header.Get("Content-Type"), "text/markdown") {
		t.Fatalf("%d %s %s", res.StatusCode, res.Header.Get("Content-Type"), md)
	}
	if !strings.Contains(string(md), "No GitHub action occurred.") || !strings.Contains(string(md), "not a recovery input") {
		t.Fatalf("markdown %s", md)
	}
}

func TestSessionReceiptListsOperatorEnvActions(t *testing.T) {
	t.Parallel()
	_, hs, e := consoleEnv(t)
	sid := seedSessionReview(t, e.Store, "example/test-repo", 8, "keep", "")
	if err := store.InsertAction(e.Store, store.Action{
		ID: "put-1", SessionID: &sid, Repo: "example/test-repo", Item: 8,
		Type: "workspace.put", ReasonCode: "recorded", EvidenceClass: "plane_observed",
		LimitSentence: "The operator copied a file into the session workspace.",
		Body:          `{"path":"shot.png"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertAction(e.Store, store.Action{
		ID: "mint-1", SessionID: &sid, Repo: "example/test-repo", Item: 8,
		Type: "preview.mint", ReasonCode: "recorded", EvidenceClass: "plane_observed",
		LimitSentence: "The operator minted a preview grant for this session.",
		Body:          `{"session_id":` + strconv.FormatInt(sid, 10) + `,"port":3000,"expires_at":"2026-11-20T00:00:00Z"}`,
	}); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, hs.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/receipt", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer op-tok")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	var exp receipt.Export
	if err := json.Unmarshal(body, &exp); err != nil {
		t.Fatal(err)
	}
	var put, mint *receipt.Action
	for i := range exp.Actions {
		switch exp.Actions[i].Type {
		case "workspace.put":
			put = &exp.Actions[i]
		case "preview.mint":
			mint = &exp.Actions[i]
		}
	}
	if put == nil || !strings.Contains(put.Body, `"path":"shot.png"`) {
		t.Fatalf("missing workspace.put %+v", exp.Actions)
	}
	if mint == nil || !strings.Contains(mint.Body, `"port":3000`) || !strings.Contains(mint.Body, `"expires_at"`) {
		t.Fatalf("missing preview.mint %+v", exp.Actions)
	}
}

func TestSessionReceiptAuthAndMissingReview(t *testing.T) {
	t.Parallel()
	_, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "no review yet", "")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, hs.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/receipt", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth %d", res.StatusCode)
	}
	req.Header.Set("Authorization", "Bearer op-tok")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound || !strings.Contains(string(body), "no completed review") {
		t.Fatalf("missing review %d %s", res.StatusCode, body)
	}
}

func seedSessionReview(t *testing.T, s *store.Store, repo string, item int, verdict, actionType string) int64 {
	t.Helper()
	now := "2026-10-30T00:00:00Z"
	var sid int64
	if err := s.DB.QueryRow(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, project, created_at) VALUES (1,'review',?,?, 'pull','completed','test',?) RETURNING id`, repo, item, now).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	var tid int64
	if err := s.DB.QueryRow(`INSERT INTO turns (session_id, lane, pending_revision, claimed_revision, lease_generation, state) VALUES (?, 'review', 1, 1, 1, 'completed') RETURNING id`, sid).Scan(&tid); err != nil {
		t.Fatal(err)
	}
	payload := `{"schema_version":1,"repo":"` + repo + `","item":` + strconv.Itoa(item) + `,"item_kind":"pull","claimed_revision":1,"verdict":"` + verdict + `"}`
	var revID int64
	if err := s.DB.QueryRow(`INSERT INTO review_revisions (job_id, claimed_revision, item_hash, main_sha, payload) VALUES (?, 1, 'h', 'm', ?) RETURNING id`, tid, payload).Scan(&revID); err != nil {
		t.Fatal(err)
	}
	if actionType != "" {
		if err := store.InsertAction(s, store.Action{
			ID: "act-receipt", SessionID: &sid, TurnID: &tid, ReviewRevisionID: &revID,
			Repo: repo, Item: item, Type: actionType, ReasonCode: "note",
			EvidenceClass: "model_assertion", LimitSentence: "limit", Body: "dry-run",
		}); err != nil {
			t.Fatal(err)
		}
	}
	return sid
}

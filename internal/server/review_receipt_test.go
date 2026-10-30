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

func TestSessionReceiptAuthAndMissingReview(t *testing.T) {
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
	res, err := s.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, project, created_at) VALUES (1,'review',?,?, 'pull','completed','test',?)`, repo, item, now)
	if err != nil {
		t.Fatal(err)
	}
	sid, _ := res.LastInsertId()
	turn, err := s.DB.Exec(`INSERT INTO turns (session_id, lane, pending_revision, claimed_revision, lease_generation, state) VALUES (?, 'review', 1, 1, 1, 'completed')`, sid)
	if err != nil {
		t.Fatal(err)
	}
	tid, _ := turn.LastInsertId()
	payload := `{"schema_version":1,"repo":"` + repo + `","item":` + strconv.Itoa(item) + `,"item_kind":"pull","claimed_revision":1,"verdict":"` + verdict + `"}`
	rev, err := s.DB.Exec(`INSERT INTO review_revisions (job_id, claimed_revision, item_hash, main_sha, payload) VALUES (?, 1, 'h', 'm', ?)`, tid, payload)
	if err != nil {
		t.Fatal(err)
	}
	revID, _ := rev.LastInsertId()
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

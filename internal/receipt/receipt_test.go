package receipt

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

func TestExportReceiptFromStoredReview(t *testing.T) {
	s := openStore(t)
	sid, revID := seedReview(t, s, seed{
		verdict: "propose_close", evidence: "server_verified",
		actionType: "close", reason: "implemented_on_main",
		body:  "dry-run close evidence_class=server_verified. verified on default branch.",
		inTok: 11, outTok: 7,
	})
	exp, err := FromStore(s, sid, nil)
	if err != nil {
		t.Fatal(err)
	}
	if exp.SchemaVersion != SchemaVersion || exp.RecoveryInput || exp.Signed {
		t.Fatalf("schema %+v", exp)
	}
	if exp.SessionID != sid || exp.ReviewRevisionID != revID || exp.TurnID == 0 {
		t.Fatalf("ids %+v", exp)
	}
	if exp.Verdict != "propose_close" || exp.EvidenceClass != "server_verified" {
		t.Fatalf("verdict %+v", exp)
	}
	if exp.SourceGitPin != "abcdef1" || exp.GuestName != "shikigami" || exp.GuestSessionID != "acp-1" {
		t.Fatalf("identity %+v", exp)
	}
	if exp.PolicyRevisionID == 0 || exp.PolicyRevision == "" {
		t.Fatalf("policy %+v", exp)
	}
	if exp.Usage == nil || *exp.Usage.InputTokens != 11 || *exp.Usage.OutputTokens != 7 {
		t.Fatalf("usage %+v", exp.Usage)
	}
	if exp.GitHubAction.Status != "dry-run" || !strings.Contains(exp.GitHubAction.Detail, "Dry-run") {
		t.Fatalf("github %+v", exp.GitHubAction)
	}
	if len(exp.Actions) != 1 || exp.Actions[0].Type != "close" || exp.Actions[0].EvidenceClass != "server_verified" {
		t.Fatalf("actions %+v", exp.Actions)
	}
	raw, err := exp.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"recovery_input": true`) || strings.Contains(string(raw), `"signed": true`) {
		t.Fatalf("json %s", raw)
	}
	md := exp.Markdown()
	if !strings.Contains(md, "server_verified (plane-recorded)") || !strings.Contains(md, "Dry-run only") {
		t.Fatalf("markdown %s", md)
	}
}

func TestExportReceiptIsNotRecoveryInput(t *testing.T) {
	s := openStore(t)
	sid, revID := seedReview(t, s, seed{verdict: "keep"})
	exp, err := FromStore(s, sid, nil)
	if err != nil {
		t.Fatal(err)
	}
	if exp.RecoveryInput {
		t.Fatal("export must not be a recovery input")
	}
	dir := t.TempDir()
	if err := WriteFiles(dir, exp, nil); err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(dir, "r"+itoa(revID)+".json")
	mdPath := filepath.Join(dir, "r"+itoa(revID)+".md")
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	var got Export
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.RecoveryInput || got.Signed {
		t.Fatalf("written json %+v", got)
	}
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "not a recovery input") {
		t.Fatalf("markdown %s", md)
	}
	if _, err := store.Open(jsonPath); err == nil {
		t.Fatal("receipt json must not open as a store")
	}
}

func TestExportReceiptKeepSaysNoGitHubAction(t *testing.T) {
	s := openStore(t)
	sid, _ := seedReview(t, s, seed{verdict: "keep"})
	exp, err := FromStore(s, sid, nil)
	if err != nil {
		t.Fatal(err)
	}
	if exp.GitHubAction.Status != "none" || exp.GitHubAction.Detail != "No GitHub action occurred." {
		t.Fatalf("%+v", exp.GitHubAction)
	}
	if !strings.Contains(exp.Markdown(), "No GitHub action occurred.") {
		t.Fatalf("markdown %s", exp.Markdown())
	}
}

func TestExportReceiptMarkdownDistinguishesEvidenceAndDryRun(t *testing.T) {
	s := openStore(t)
	sid, _ := seedReview(t, s, seed{
		verdict: "propose_comment", evidence: "model_assertion",
		actionType: "comment", reason: "note",
		body: "dry-run comment evidence_class=model_assertion. not eligible to apply",
	})
	exp, err := FromStore(s, sid, nil)
	if err != nil {
		t.Fatal(err)
	}
	if exp.EvidenceClass != "model_assertion" || exp.GitHubAction.Status != "dry-run" {
		t.Fatalf("%+v", exp)
	}
	md := exp.Markdown()
	if !strings.Contains(md, "model_assertion (guest-claimed, not proof)") {
		t.Fatalf("markdown %s", md)
	}
	if !strings.Contains(md, "server_verified") || !strings.Contains(md, "model_assertion") {
		t.Fatal("markdown must define both evidence classes")
	}
}

func TestExportReceiptRedactsSecretsAndOmitsPrompt(t *testing.T) {
	s := openStore(t)
	secret := "supersecret-token-value"
	sid, _ := seedReview(t, s, seed{
		verdict: "keep", body: "see " + secret, prompt: "private prompt " + secret,
		guestConfig: "KEY=" + secret,
	})
	exp, err := FromStore(s, sid, []string{secret})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(exp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) || strings.Contains(exp.Markdown(), secret) {
		t.Fatalf("leaked secret in %s", raw)
	}
	if strings.Contains(string(raw), "private prompt") || strings.Contains(string(raw), "KEY=") {
		t.Fatalf("prompt or guest config leaked: %s", raw)
	}
}

func TestExportReceiptUnsigned(t *testing.T) {
	s := openStore(t)
	sid, _ := seedReview(t, s, seed{verdict: "keep"})
	exp, err := FromStore(s, sid, nil)
	if err != nil {
		t.Fatal(err)
	}
	if exp.Signed {
		t.Fatal("signed portable bundles are excluded from 1.0")
	}
	raw, _ := exp.JSON()
	if strings.Contains(string(raw), `"mac"`) || strings.Contains(string(raw), "HMAC") {
		t.Fatalf("unexpected signature material %s", raw)
	}
	if !strings.Contains(exp.Markdown(), "Unsigned") {
		t.Fatalf("markdown %s", exp.Markdown())
	}
}

func TestFromStoreMissingReview(t *testing.T) {
	s := openStore(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1,'review','o/r',1,'pull','open',?)`, now)
	if err != nil {
		t.Fatal(err)
	}
	sid, _ := res.LastInsertId()
	_, err = FromStore(s, sid, nil)
	if !errors.Is(err, ErrNoReview) {
		t.Fatalf("got %v", err)
	}
}

type seed struct {
	verdict, evidence, actionType, reason, body, prompt, guestConfig string
	inTok, outTok                                                    int
}

func seedReview(t *testing.T, s *store.Store, in seed) (sessionID, revisionID int64) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.DB.Exec(`INSERT INTO policy_revisions (hash, payload) VALUES ('pol-hash', 'version: 2\nprojects: {}\n')`); err != nil {
		t.Fatal(err)
	}
	prompt := in.prompt
	if prompt == "" {
		prompt = "do not export"
	}
	guestCfg := in.guestConfig
	if guestCfg == "" {
		guestCfg = "cfg"
	}
	res, err := s.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, project, prompt, guest_name, guest_pin, guest_config, guest_session_id, created_at) VALUES (1,'review','o/r',42,'pull','completed','demo',?,?,?,?,?,?)`,
		prompt, "shikigami", "pin-1", guestCfg, "sess-db", now)
	if err != nil {
		t.Fatal(err)
	}
	sid, _ := res.LastInsertId()
	turnRes, err := s.DB.Exec(`INSERT INTO turns (session_id, lane, pending_revision, claimed_revision, lease_generation, state) VALUES (?, 'review', 1, 1, 3, 'completed')`, sid)
	if err != nil {
		t.Fatal(err)
	}
	tid, _ := turnRes.LastInsertId()
	snap := `{"repo":"o/r","item":42,"item_kind":"pull","head_sha":"abcdef1","main_sha":"mainsha"}`
	if _, err := s.DB.Exec(`INSERT INTO snapshots (repo, item, revision, item_kind, item_hash, main_sha, payload) VALUES ('o/r', 42, 1, 'pull', 'h', 'mainsha', ?)`, snap); err != nil {
		t.Fatal(err)
	}
	if in.evidence == "" {
		in.evidence = "model_assertion"
	}
	art := map[string]any{
		"schema_version": 1, "repo": "o/r", "item": 42, "item_kind": "pull",
		"claimed_revision": 1, "snapshot_hash": "h", "main_sha": "mainsha", "head_sha": "abcdef1",
		"verdict": in.verdict, "guest_session_id": "acp-1",
		"proposed_actions": []map[string]any{
			{"type": empty(in.actionType, "none"), "evidence": []map[string]string{{"class": in.evidence}}},
		},
	}
	if in.inTok != 0 {
		art["input_tokens"] = in.inTok
	}
	if in.outTok != 0 {
		art["output_tokens"] = in.outTok
	}
	payload, err := json.Marshal(art)
	if err != nil {
		t.Fatal(err)
	}
	revRes, err := s.DB.Exec(`INSERT INTO review_revisions (job_id, claimed_revision, item_hash, main_sha, payload) VALUES (?, 1, 'h', 'mainsha', ?)`, tid, string(payload))
	if err != nil {
		t.Fatal(err)
	}
	revID, _ := revRes.LastInsertId()
	if in.actionType != "" {
		if err := store.InsertAction(s, store.Action{
			ID: "act-1", SessionID: &sid, TurnID: &tid, ReviewRevisionID: &revID,
			Repo: "o/r", Item: 42, Type: in.actionType, ReasonCode: in.reason,
			EvidenceClass: in.evidence, LimitSentence: "limit", Body: in.body,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return sid, revID
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "rusui.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func empty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

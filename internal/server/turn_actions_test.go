package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/store"
)

func TestTurnActionIngest(t *testing.T) {
	e, clk, hs := eventEnv(t)
	sid, turnID := leasedActionTurn(t, e, clk, 4)
	sec, err := issueTurnSecrets(e.Store, turnID, 1, clk.T)
	if err != nil {
		t.Fatal(err)
	}

	update := `{"type":"acp.session.update","reason":"recorded","body":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello ` + sec.Runner + ` ghp_abcdefghijklmnopqrstuvwxyz0123456789"}}}}`
	if code := postTurnAction(t, hs, turnID, sec.Runner, update); code != http.StatusAccepted {
		t.Fatalf("code %d", code)
	}
	acts, err := store.ListActions(e.Store, "example/test-repo", 4)
	if err != nil || len(acts) != 1 {
		t.Fatalf("actions %d %v", len(acts), err)
	}
	if acts[0].Type != "acp.session.update" || acts[0].TurnID == nil || *acts[0].TurnID != turnID {
		t.Fatalf("%+v", acts[0])
	}
	if acts[0].SessionID == nil || *acts[0].SessionID != sid {
		t.Fatalf("session %v", acts[0].SessionID)
	}
	if strings.Contains(acts[0].Body, sec.Runner) || strings.Contains(acts[0].Body, "ghp_") {
		t.Fatalf("unredacted body %s", acts[0].Body)
	}
	if !strings.Contains(acts[0].Body, `[redacted]`) || !strings.Contains(acts[0].Body, `"sessionUpdate":"agent_message_chunk"`) {
		t.Fatalf("normalized body %s", acts[0].Body)
	}

	if code := postTurnAction(t, hs, turnID, sec.Guest, `{"type":"acp.session.update","reason":"recorded","body":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"forged"}}}}`); code != http.StatusUnauthorized {
		t.Fatalf("guest grant posted actions %d", code)
	}

	if code := postTurnAction(t, hs, turnID, sec.Runner, `{"type":"forged.transcript"}`); code != http.StatusBadRequest {
		t.Fatalf("unknown type %d", code)
	}

	if code := postTurnAction(t, hs, turnID, "", `{"type":"acp.fs.read_text_file"}`); code != http.StatusUnauthorized {
		t.Fatalf("unauth %d", code)
	}

	if code := postTurnAction(t, hs, 9999, sec.Runner, `{"type":"acp.unknown"}`); code != http.StatusNotFound {
		t.Fatalf("missing turn %d", code)
	}
}

func TestTurnActionBodyCap(t *testing.T) {
	e, clk, hs := eventEnv(t)
	_, turnID := leasedActionTurn(t, e, clk, 5)
	sec, err := issueTurnSecrets(e.Store, turnID, 1, clk.T)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"type":"resume","reason":"succeeded","body":{"pad":"` + strings.Repeat("a", turnActionBodyCap) + `"}}`
	if code := postTurnAction(t, hs, turnID, sec.Runner, body); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize %d", code)
	}
}

func TestTurnActionBudget(t *testing.T) {
	e, clk, hs := eventEnv(t)
	_, turnID := leasedActionTurn(t, e, clk, 6)
	sec, err := issueTurnSecrets(e.Store, turnID, 1, clk.T)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := store.GetTurn(e.Store, turnID)
	if err != nil {
		t.Fatal(err)
	}
	sid := turn.SessionID
	for i := range engine.TurnActionRowCap {
		if err := store.InsertAction(e.Store, store.Action{
			ID: "fill-" + strconv.Itoa(i), SessionID: &sid, TurnID: &turnID,
			Repo: "example/test-repo", Item: 6, Type: "resume", ReasonCode: "recorded",
			EvidenceClass: "plane_observed", Body: "{}",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if code := postTurnAction(t, hs, turnID, sec.Runner, `{"type":"resume","reason":"succeeded"}`); code != http.StatusTooManyRequests {
		t.Fatalf("budget %d", code)
	}
}

func TestGuestGrantCannotCompleteTurn(t *testing.T) {
	e, clk, hs := eventEnv(t)
	_, turnID := leasedActionTurn(t, e, clk, 7)
	sec, err := issueTurnSecrets(e.Store, turnID, 1, clk.T)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", hs.URL+"/jobs/"+strconv.FormatInt(turnID, 10)+"/complete", strings.NewReader(`{"lease_generation":1,"claimed_revision":0}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+sec.Guest)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("guest complete %d", res.StatusCode)
	}
}

func leasedActionTurn(t *testing.T, e *engine.Engine, clk *clock.Fake, item int) (sessionID, turnID int64) {
	t.Helper()
	now := clk.T.UTC().Format(time.RFC3339Nano)
	res, err := e.Store.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1,'review','example/test-repo',?,'issue','open',?)`, item, now)
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err = res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Store.DB.Exec(`INSERT INTO turns (session_id, lane, state) VALUES (?, 'review', 'leased')`, sessionID); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.DB.QueryRow(`SELECT id FROM turns WHERE session_id=?`, sessionID).Scan(&turnID); err != nil {
		t.Fatal(err)
	}
	return sessionID, turnID
}

func postTurnAction(t *testing.T, hs *httptest.Server, turnID int64, token, body string) int {
	t.Helper()
	req, err := http.NewRequest("POST", hs.URL+"/turns/"+strconv.FormatInt(turnID, 10)+"/actions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	return res.StatusCode
}

package server

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

func TestTurnActionIngest(t *testing.T) {
	e, clk, hs := eventEnv(t)
	now := clk.T.UTC().Format(time.RFC3339Nano)
	res, err := e.Store.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1,'review','example/test-repo',4,'issue','open',?)`, now)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Store.DB.Exec(`INSERT INTO turns (session_id, lane, state) VALUES (?, 'review', 'leased')`, sid); err != nil {
		t.Fatal(err)
	}
	var turnID int64
	if err := e.Store.DB.QueryRow(`SELECT id FROM turns WHERE session_id=?`, sid).Scan(&turnID); err != nil {
		t.Fatal(err)
	}
	tok, _, err := issueTurnToken(e.Store, turnID, 1, clk.T)
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest("POST", hs.URL+"/turns/"+strconv.FormatInt(turnID, 10)+"/actions", strings.NewReader(`{"type":"acp.session.update","reason":"recorded","body":{"k":"v"}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resHTTP, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resHTTP.Body.Close()
	if resHTTP.StatusCode != http.StatusAccepted {
		t.Fatalf("code %d", resHTTP.StatusCode)
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

	bad, err := http.NewRequest("POST", hs.URL+"/turns/"+strconv.FormatInt(turnID, 10)+"/actions", strings.NewReader(`{"type":"acp.fs.read_text_file"}`))
	if err != nil {
		t.Fatal(err)
	}
	resHTTP, err = http.DefaultClient.Do(bad)
	if err != nil {
		t.Fatal(err)
	}
	_ = resHTTP.Body.Close()
	if resHTTP.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth %d", resHTTP.StatusCode)
	}

	missing, err := http.NewRequest("POST", hs.URL+"/turns/9999/actions", strings.NewReader(`{"type":"acp.unknown"}`))
	if err != nil {
		t.Fatal(err)
	}
	missing.Header.Set("Authorization", "Bearer "+tok)
	resHTTP, err = http.DefaultClient.Do(missing)
	if err != nil {
		t.Fatal(err)
	}
	_ = resHTTP.Body.Close()
	if resHTTP.StatusCode != http.StatusNotFound {
		t.Fatalf("missing turn %d", resHTTP.StatusCode)
	}
}

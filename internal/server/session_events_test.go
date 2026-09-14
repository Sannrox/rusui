package server

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

func TestGuestSessionEventIngest(t *testing.T) {
	e, clk, hs := eventEnv(t)
	now := clk.T.UTC().Format(time.RFC3339Nano)
	res, err := e.Store.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1,'review','example/test-repo',3,'issue','open',?)`, now)
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

	req, err := http.NewRequest("POST", hs.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/events", strings.NewReader(`{"delivery_id":"g1","kind":"terminal"}`))
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
	n, err := store.CountEvents(e.Store, "example/test-repo", 3)
	if err != nil || n != 1 {
		t.Fatalf("events %d %v", n, err)
	}
	var source string
	var sessionID int64
	if err := e.Store.DB.QueryRow(`SELECT source, session_id FROM events WHERE delivery_id='g1'`).Scan(&source, &sessionID); err != nil {
		t.Fatal(err)
	}
	if source != "guest" || sessionID != sid {
		t.Fatalf("source %s session %d", source, sessionID)
	}

	bad, err := http.NewRequest("POST", hs.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/events", strings.NewReader(`{"delivery_id":"g2"}`))
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

	missing, err := http.NewRequest("POST", hs.URL+"/sessions/9999/events", strings.NewReader(`{"delivery_id":"g3"}`))
	if err != nil {
		t.Fatal(err)
	}
	missing.Header.Set("Authorization", "Bearer wsec")
	// eventEnv has WebhookSec only — worker secret empty so 404 before auth? GetSession 404 first.
	resHTTP, err = http.DefaultClient.Do(missing)
	if err != nil {
		t.Fatal(err)
	}
	_ = resHTTP.Body.Close()
	if resHTTP.StatusCode != http.StatusNotFound {
		t.Fatalf("missing session %d", resHTTP.StatusCode)
	}
}

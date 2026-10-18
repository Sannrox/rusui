package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"database/sql"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/store"
)

func TestHaltHTTPRequiresWorkerAndStopsClaims(t *testing.T) {
	e, _, _ := eventEnv(t)
	hs := httptest.NewServer((&Server{Eng: e, WorkerSec: "wsec"}).Handler())
	t.Cleanup(hs.Close)

	req, _ := http.NewRequest("POST", hs.URL+"/halt", strings.NewReader(`{"scope":"agent","target":"alice"}`))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth %d", res.StatusCode)
	}

	req, _ = http.NewRequest("POST", hs.URL+"/freeze", strings.NewReader(`{"scope":"agent","target":"alice","reason":"freeze"}`))
	req.Header.Set("Authorization", "Bearer wsec")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("freeze %d %s", res.StatusCode, body)
	}
	var rep engine.HaltReport
	if err := json.Unmarshal(body, &rep); err != nil || !rep.Halted || rep.Scope != "agent" {
		t.Fatalf("%s %v", body, err)
	}
	var halted bool
	if err := e.Store.Tx(func(tx *sql.Tx) error {
		on, err := store.Halted(tx, store.DefaultTenantID, "test", "alice")
		halted = on
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !halted {
		t.Fatal("agent halt not set")
	}

	req, _ = http.NewRequest("POST", hs.URL+"/unhalt", strings.NewReader(`{"scope":"agent","target":"alice"}`))
	req.Header.Set("Authorization", "Bearer wsec")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("unhalt %d", res.StatusCode)
	}
	if err := e.Store.Tx(func(tx *sql.Tx) error {
		on, err := store.Halted(tx, store.DefaultTenantID, "test", "alice")
		halted = on
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if halted {
		t.Fatal("agent halt still set")
	}
}

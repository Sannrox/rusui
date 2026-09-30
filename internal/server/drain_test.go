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

func TestDrainHTTPRequiresWorkerAndStopsClaims(t *testing.T) {
	e, _, _ := eventEnv(t)
	hs := httptest.NewServer((&Server{Eng: e, WorkerSec: "wsec"}).Handler())
	t.Cleanup(hs.Close)

	req, _ := http.NewRequest("POST", hs.URL+"/drain", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth %d", res.StatusCode)
	}

	req, _ = http.NewRequest("POST", hs.URL+"/drain", nil)
	req.Header.Set("Authorization", "Bearer wsec")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("drain %d %s", res.StatusCode, body)
	}
	var rep engine.DrainReport
	if err := json.Unmarshal(body, &rep); err != nil || !rep.Paused {
		t.Fatalf("%s %v", body, err)
	}
	if strings.Contains(string(body), "wsec") {
		t.Fatal("token leaked")
	}
	var paused bool
	if err := e.Store.Tx(func(tx *sql.Tx) error {
		on, err := store.Paused(tx, "")
		paused = on
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !paused {
		t.Fatal("global pause not set")
	}
}

// Resume lifts drain's pause without Slack; it needs the worker token.
func TestResumeHTTPLiftsDrainPause(t *testing.T) {
	e, _, _ := eventEnv(t)
	hs := httptest.NewServer((&Server{Eng: e, WorkerSec: "wsec"}).Handler())
	t.Cleanup(hs.Close)
	if _, err := e.Drain(); err != nil {
		t.Fatal(err)
	}
	post := func(path, token string) int {
		req, _ := http.NewRequest("POST", hs.URL+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	paused := func() bool {
		var on bool
		if err := e.Store.Tx(func(tx *sql.Tx) error {
			var err error
			on, err = store.Paused(tx, "")
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return on
	}
	if code := post("/resume", ""); code != http.StatusUnauthorized || !paused() {
		t.Fatalf("unauth resume %d paused=%v", code, paused())
	}
	if code := post("/resume?project=nope", "wsec"); code != http.StatusBadRequest || !paused() {
		t.Fatalf("unknown project %d paused=%v", code, paused())
	}
	if code := post("/resume", "wsec"); code != http.StatusOK || paused() {
		t.Fatalf("resume %d paused=%v", code, paused())
	}
}

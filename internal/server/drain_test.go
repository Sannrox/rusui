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

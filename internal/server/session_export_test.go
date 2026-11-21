package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

func TestSessionExportRefusesGuest(t *testing.T) {
	t.Parallel()
	_, hs, e := consoleEnv(t)
	e.SessionExportKey = bytes.Repeat([]byte("k"), 32)
	sid, err := e.StartRun("test", "hello", "")
	if err != nil {
		t.Fatal(err)
	}
	path := hs.URL + "/sessions/" + strconv.FormatInt(sid, 10) + "/export-session"
	req, err := http.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth %d", res.StatusCode)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("worker %d", res.StatusCode)
	}
	imp, err := http.NewRequest(http.MethodPost, hs.URL+"/import-session?project=test", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	imp.Header.Set("Authorization", "Bearer wsec")
	res, err = http.DefaultClient.Do(imp)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("worker import %d", res.StatusCode)
	}
}

func TestSessionExportOperatorRoundTrip(t *testing.T) {
	t.Parallel()
	_, hs, e := consoleEnv(t)
	e.SessionExportKey = bytes.Repeat([]byte("k"), 32)
	planeID, err := store.PlaneID(e.Store)
	if err != nil {
		t.Fatal(err)
	}
	p, err := policy.Parse([]byte(strings.Replace(slackPol, "projects:\n", "session_export_sources: ["+planeID+"]\nprojects:\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	e.ReloadPolicy(p)
	sid, err := e.StartRun("test", "hello", "")
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, hs.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/export-session", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer op-tok")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("export %d %s", res.StatusCode, body)
	}
	imp, err := http.NewRequest(http.MethodPost, hs.URL+"/import-session?project=test", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	imp.Header.Set("Authorization", "Bearer op-tok")
	res, err = http.DefaultClient.Do(imp)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("import %d %s", res.StatusCode, out)
	}
	var got struct {
		SessionID int64 `json:"session_id"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.SessionID == 0 || got.SessionID == sid {
		t.Fatalf("imported %+v source %d", got, sid)
	}
}

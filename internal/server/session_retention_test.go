package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/store"
)

func TestRetentionHTTPOverrideAndDelete(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "keep-secret", "")
	if err != nil {
		t.Fatal(err)
	}
	path := "/sessions/" + strconv.FormatInt(sid, 10)
	raw := getJSON(t, hs.URL+path+"/tools", "op-tok")
	if !strings.Contains(raw, `"retention":"short"`) {
		t.Fatalf("default tools %s", raw)
	}
	if code := postJSONStatus(t, hs.URL+path+"/retention/write-override", "op-tok", `{"reason":"need write"}`); code != http.StatusConflict {
		t.Fatalf("short override %d", code)
	}
	if code := putJSONStatus(t, hs.URL+path+"/retention", "op-tok", `{"class":"indefinite"}`); code != http.StatusOK {
		t.Fatalf("class %d", code)
	}
	if code := postJSONStatus(t, hs.URL+path+"/retention/write-override", "op-tok", `{"reason":""}`); code != http.StatusBadRequest {
		t.Fatalf("empty reason %d", code)
	}
	if code := postJSONStatus(t, hs.URL+path+"/retention/write-override", "op-tok", `{"reason":"need write"}`); code != http.StatusCreated {
		t.Fatalf("override %d", code)
	}
	tools := getJSON(t, hs.URL+path+"/tools", "op-tok")
	if !strings.Contains(tools, `"retention":"indefinite"`) || !strings.Contains(tools, `"write_override":true`) {
		t.Fatalf("tools after override %s", tools)
	}
	logs := getJSON(t, hs.URL+path+"/logs", "op-tok")
	if !strings.Contains(logs, "retention.write_override") || !strings.Contains(logs, "retention.class") {
		t.Fatalf("override unaudited %s", logs)
	}
	req, err := http.NewRequest(http.MethodPost, hs.URL+path+"/retention/delete", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer op-tok")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete %d", res.StatusCode)
	}
	sess, err := store.GetSession(e.Store, sid)
	if err != nil || sess.Prompt != "" || !sess.RetentionExpired {
		t.Fatalf("deleted %+v %v", sess, err)
	}
	var wrap struct {
		Retention string `json:"retention"`
	}
	detail := getJSON(t, hs.URL+path, "op-tok")
	if err := json.Unmarshal([]byte(detail), &wrap); err != nil && !strings.Contains(detail, `"retention":"indefinite"`) {
		t.Fatalf("detail %s", detail)
	}
}

func putJSONStatus(t *testing.T, url, token, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	return res.StatusCode
}

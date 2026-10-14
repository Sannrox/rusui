package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/store"
)

func TestSpaceSessionDeniesWriteToolUntilGranted(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "space-tools", "")
	if err != nil {
		t.Fatal(err)
	}
	path := "/sessions/" + strconv.FormatInt(sid, 10)
	raw := getJSON(t, hs.URL+path+"/tools", "op-tok")
	if !strings.Contains(raw, `"read"`) || strings.Contains(raw, `"execute"`) {
		t.Fatalf("default tools %s", raw)
	}
	if code := postJSONStatus(t, hs.URL+path+"/tool-grants", "op-tok", `{"tool":"execute"}`); code != http.StatusCreated {
		t.Fatalf("grant %d", code)
	}
	listed := getJSON(t, hs.URL+path+"/tool-grants", "op-tok")
	if !strings.Contains(listed, `"tool":"execute"`) {
		t.Fatalf("grant list %s", listed)
	}
	detail := getJSON(t, hs.URL+path, "op-tok")
	if !strings.Contains(detail, `"execute"`) {
		t.Fatalf("session tools %s", detail)
	}
	logs := getJSON(t, hs.URL+path+"/logs", "op-tok")
	if !strings.Contains(logs, "tool.grant") {
		t.Fatalf("grant unaudited %s", logs)
	}
	if code := postJSONStatus(t, hs.URL+path+"/tool-grants", "op-tok", `{"tool":"execute"}`); code != http.StatusConflict {
		t.Fatalf("dup grant %d", code)
	}
	req, err := http.NewRequest(http.MethodDelete, hs.URL+path+"/tool-grants/execute", nil)
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
		t.Fatalf("revoke %d", res.StatusCode)
	}
	logs = getJSON(t, hs.URL+path+"/logs", "op-tok")
	if !strings.Contains(logs, "tool.revoke") {
		t.Fatalf("revoke unaudited %s", logs)
	}
	after := getJSON(t, hs.URL+path+"/tools", "op-tok")
	if strings.Contains(after, `"execute"`) {
		t.Fatalf("revoked still listed %s", after)
	}
	if code := postJSONStatus(t, hs.URL+path+"/tool-grants", "op-tok", `{"tool":"execute"}`); code != http.StatusCreated {
		t.Fatalf("regrant %d", code)
	}
	req, err = http.NewRequest(http.MethodDelete, hs.URL+path+"/tool-grants/execute", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer op-tok")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("second revoke %d", res.StatusCode)
	}
	logs = getJSON(t, hs.URL+path+"/logs", "op-tok")
	if strings.Count(logs, "tool.revoke") < 2 {
		t.Fatalf("second revoke unaudited %s", logs)
	}
}

func TestUserModeRejectsToolGrantHTTP(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sid, err := e.StartRunExec("test", "user-tools", "", "", "", "", store.ExecutionUser, "default", "default")
	if err != nil {
		t.Fatal(err)
	}
	path := "/sessions/" + strconv.FormatInt(sid, 10)
	if code := postJSONStatus(t, hs.URL+path+"/tool-grants", "op-tok", `{"tool":"write"}`); code != http.StatusConflict {
		t.Fatalf("user grant %d", code)
	}
}

func TestSpaceReadGateUsedForApprovals(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "space-approval", "")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := store.SessionHasToolGrant(e.Store, sid, "execute")
	if err != nil || ok {
		t.Fatalf("unexpected grant %v %v", ok, err)
	}
	var wrap struct {
		Tools []string `json:"tools"`
	}
	raw := getJSON(t, hs.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/tools", "op-tok")
	if err := json.Unmarshal([]byte(raw), &wrap); err != nil {
		t.Fatal(err)
	}
	for _, tname := range wrap.Tools {
		if tname == "execute" || tname == "write" {
			t.Fatalf("write tool present %v", wrap.Tools)
		}
	}
}

func TestSpaceApprovalInvalidWithoutGrant(t *testing.T) {
	e, _, _ := leasedTurn(t)
	var turnID, sessID int64
	if err := e.Store.DB.QueryRow(`SELECT id, session_id FROM turns WHERE state='leased'`).Scan(&turnID, &sessID); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertAction(e.Store, store.Action{
		ID: "act-ungranted", SessionID: &sessID, TurnID: &turnID, Repo: "example/test-repo", Item: 8,
		Type: acp.ActionApproval, ReasonCode: acp.ReasonUnmatched,
		EvidenceClass: acp.EvidenceObserved, LimitSentence: acp.LimitSentence,
		Body: `{"toolCall":{"toolCallId":"tc","kind":"execute","command":"npm test"}}`,
	}); err != nil {
		t.Fatal(err)
	}
	s := &Server{Eng: e}
	if s.allowStillValid("act-ungranted") {
		t.Fatal("ungranted execute approval stayed valid")
	}
	if _, err := store.GrantSessionTool(e.Store, sessID, "execute", "plane"); err != nil {
		t.Fatal(err)
	}
	if !s.allowStillValid("act-ungranted") {
		t.Fatal("granted execute approval became invalid")
	}
}

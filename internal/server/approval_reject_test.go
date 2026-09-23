package server

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

func TestApprovalRevalidationHonoursRejectRules(t *testing.T) {
	e, _, _ := leasedTurn(t)
	var turnID, sessID int64
	if err := e.Store.DB.QueryRow(`SELECT id, session_id FROM turns WHERE state='leased'`).Scan(&turnID, &sessID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Store.DB.Exec(`UPDATE sessions SET project='test' WHERE id=?`, sessID); err != nil {
		t.Fatal(err)
	}
	pol := strings.Replace(slackPol, "  test:\n", "  test:\n    permissions:\n      - command: npm\n      - command: npm publish\n        action: reject\n", 1)
	p, err := policy.Parse([]byte(pol))
	if err != nil {
		t.Fatal(err)
	}
	e.ReloadPolicy(p)
	s := &Server{Eng: e}
	approval := func(id, cmd string) string {
		if err := store.InsertAction(e.Store, store.Action{
			ID: id, SessionID: &sessID, TurnID: &turnID, Repo: "example/test-repo", Item: 8,
			Type: acp.ActionApproval, ReasonCode: acp.ReasonUnmatched,
			EvidenceClass: acp.EvidenceObserved, LimitSentence: acp.LimitSentence,
			Body: `{"toolCall":{"toolCallId":"tc","command":"` + cmd + `"}}`,
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	if s.allowStillValid(approval("act-publish", "npm publish --access public")) {
		t.Fatal("approval of a rejected command stayed valid")
	}
	if !s.allowStillValid(approval("act-test", "npm test")) {
		t.Fatal("approval of an allowed command became invalid")
	}
}

func TestFencedApprovalIsInvalidInImplementSessions(t *testing.T) {
	_, e := agentCredentialEnv(t, true, agentToken)
	task, err := e.StartTask("test", implementTask())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Store.DB.Exec(`UPDATE turns SET state='leased' WHERE session_id=?`, task.SessionID); err != nil {
		t.Fatal(err)
	}
	var turnID int64
	if err := e.Store.DB.QueryRow(`SELECT id FROM turns WHERE session_id=? AND state='leased' ORDER BY id DESC LIMIT 1`, task.SessionID).Scan(&turnID); err != nil {
		t.Fatal(err)
	}
	s := &Server{Eng: e, AgentGitHubToken: agentToken}
	approval := func(id, cmd string) string {
		body, _ := json.Marshal(acp.PermissionParams{ToolCall: json.RawMessage(`{"command":` + strconv.Quote(cmd) + `}`)})
		if err := store.InsertAction(e.Store, store.Action{
			ID: id, SessionID: &task.SessionID, TurnID: &turnID, Repo: "example/test-repo",
			Type: acp.ActionApproval, ReasonCode: acp.ReasonUnmatched,
			EvidenceClass: acp.EvidenceObserved, LimitSentence: acp.LimitSentence, Body: string(body),
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	if s.allowStillValid(approval("act-wrapped-merge", `bash -c 'gh pr merge 1'`)) {
		t.Fatal("approval of a fenced command stayed valid in an implement session")
	}
	if !s.allowStillValid(approval("act-create", "gh pr create --fill")) {
		t.Fatal("approval of an allowed command became invalid")
	}
}

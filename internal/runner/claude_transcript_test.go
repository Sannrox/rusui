package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/provider"
)

type receiptLog struct {
	mu   sync.Mutex
	rows []acp.Receipt
}

func (l *receiptLog) Record(rec acp.Receipt) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rows = append(l.rows, rec)
	return fmt.Sprintf("act-%d", len(l.rows)), nil
}

// A Claude turn records its assistant text and each tool call as
// acp.session.update rows, in the shape an ACP guest's rows take, beside
// its permission rows. No token the turn holds reaches those rows.
func TestHostACPClaudeRecordsTranscriptAndToolCalls(t *testing.T) {
	const (
		turnToken = "rusui-turn-grant-5d0c7a9e1f2b4c6d"
		ghToken   = "ghp_R4nd0mLook1ngT0kenValue0123456789"
		envToken  = "env-secret-value-8a7b6c5d4e3f"
		bareToken = "github_pat_11AAAAAAA0abcdefghijklmnopqrstuvwxyz0123456789"
	)
	t.Setenv("RUSUI_WORKER_SECRET", envToken)
	quote := func(s string) string { raw, _ := json.Marshal(s); return string(raw) }
	stream := []string{
		`{"type":"assistant","message":{"id":"msg_01A","type":"message","role":"assistant","model":"claude-sonnet-5","content":[{"type":"text","text":"I'll list the workspace."}],"stop_reason":null},"parent_tool_use_id":null,"session_id":"claude-sess","uuid":"u1"}`,
		`{"type":"assistant","message":{"id":"msg_01B","type":"message","role":"assistant","model":"claude-sonnet-5","content":[{"type":"tool_use","id":"toolu_01ABC","name":"Bash","input":{"command":` +
			quote("curl -H 'Authorization: Bearer "+turnToken+"' https://plane/x && GH_TOKEN="+ghToken+" gh pr list && echo "+bareToken) +
			`,"description":"List pull requests"}}],"stop_reason":null},"parent_tool_use_id":null,"session_id":"claude-sess","uuid":"u2"}`,
		`{"type":"control_request","request_id":"req-1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":` + quote("echo "+turnToken) + `}}}`,
		`{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_01ABC","type":"tool_result","content":` +
			quote("RUSUI_TURN_TOKEN="+turnToken+"\nGH_TOKEN="+ghToken+"\nRUSUI_WORKER_SECRET="+envToken) +
			`,"is_error":false}]},"parent_tool_use_id":null,"session_id":"claude-sess","uuid":"u3","tool_use_result":{"stdout":"x","stderr":"","interrupted":false,"isImage":false}}`,
		`{"type":"assistant","message":{"id":"msg_01C","type":"message","role":"assistant","model":"claude-sonnet-5","content":[{"type":"text","text":` +
			quote("The grant is "+turnToken+", the worker secret is "+envToken+" and the PAT is "+ghToken+".") +
			`}],"stop_reason":"end_turn"},"parent_tool_use_id":null,"session_id":"claude-sess","uuid":"u4"}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"claude-sess"}`,
	}
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	go func() {
		r := bufio.NewReader(agentIn)
		if _, err := r.ReadBytes('\n'); err != nil {
			return
		}
		_, _ = fmt.Fprintf(agentOut, `{"type":"system","subtype":"init","claude_code_version":%q,"session_id":"claude-sess"}`+"\n", provider.ClaudeCodeVersion)
		for _, line := range stream {
			_, _ = io.WriteString(agentOut, line+"\n")
			if strings.Contains(line, `"control_request"`) {
				if _, err := r.ReadBytes('\n'); err != nil {
					return
				}
			}
		}
	}()
	rec := &receiptLog{}
	host := &acp.Client{In: clientIn, Out: clientOut, Rec: rec, Wait: func(context.Context, string, acp.PermissionParams) acp.Decision {
		return acp.Decision{}
	}}
	in, _ := json.Marshal(map[string]string{"body": "list pull requests"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a := &Assignment{Guest: acp.GuestClaude, Input: in, Repo: "example/test-repo", Item: 1, ItemKind: "run", TurnToken: turnToken, GitHubToken: ghToken}
	if _, err := HostACP(ctx, a, host, t.TempDir()); err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, row := range rec.rows {
		raw, _ := json.Marshal(row.Body)
		if row.Type != acp.ActionUpdate {
			got = append(got, row.Type)
			continue
		}
		for _, secret := range []string{turnToken, ghToken, envToken, bareToken} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("row carries a secret: %s", raw)
			}
		}

		var body struct {
			Update map[string]any `json:"update"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		u := body.Update
		switch u["sessionUpdate"] {
		case "agent_message_chunk":
			content, _ := u["content"].(map[string]any)
			got = append(got, fmt.Sprintf("text %v", content["text"]))
		case "tool_call":
			input, _ := u["rawInput"].(map[string]any)
			got = append(got, fmt.Sprintf("tool_call %v %v %v %v %v", u["toolCallId"], u["title"], u["kind"], u["status"], input["command"]))
		case "tool_call_update":
			got = append(got, fmt.Sprintf("tool_call_update %v %v %v", u["toolCallId"], u["status"], u["content"]))
		default:
			t.Fatalf("update %v", u)
		}
	}
	want := []string{
		"text I'll list the workspace.",
		"tool_call toolu_01ABC Bash execute pending curl -H 'Authorization: Bearer [redacted]' https://plane/x && GH_TOKEN=[redacted] gh pr list && echo [redacted]",
		acp.ActionPermission,
		acp.ActionApproval,
		"tool_call_update toolu_01ABC completed <nil>",
		"text The grant is [redacted], the worker secret is [redacted] and the PAT is [redacted].",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

package provider

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestClaudeLoopDropsTranscriptBodies(t *testing.T) {
	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = pw.Close() }()
		for range 50 {
			_, _ = fmt.Fprintf(pw, `{"type":"assistant","message":{"content":[{"type":"text","text":"%s"}]}}`+"\n", strings.Repeat("x", 200))
		}
		_, _ = fmt.Fprintln(pw, `{"type":"result","session_id":"sess-1"}`)
	}()
	observed := 0
	res, err := readProviderLoop(bufio.NewReader(pr), io.Discard, "sess-1", nil, "claude", func(Event) { observed++ })
	if err != nil {
		t.Fatal(err)
	}
	if res.Cursor != "sess-1" {
		t.Fatalf("cursor %q", res.Cursor)
	}
	if len(res.Events) != 0 {
		t.Fatalf("claude host keeps Cursor only, events %+v", res.Events)
	}
	if observed != 50 {
		t.Fatalf("observed %d transcript events, want 50", observed)
	}
}

// A numeric JSON-RPC request id must come back numeric, not as a quoted
// string: a mismatched id type is an invalid reply under the spec and
// providers that compare ids by raw value would never match it.
func TestPermissionReplyEchoesNumericIDUnchanged(t *testing.T) {
	for _, tc := range []struct {
		kind   string
		method string
	}{
		{"grok", "session/request_permission"},
		{"codex", "item/commandExecution/requestApproval"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			pr, pw := io.Pipe()
			go func() {
				defer func() { _ = pw.Close() }()
				_, _ = fmt.Fprintf(pw, `{"id":7,"method":%q,"params":{"options":[{"id":"allow-once"},{"id":"reject-once"}]}}`+"\n", tc.method)
				_, _ = fmt.Fprintln(pw, `{"method":"turn/completed"}`)
			}()
			var out strings.Builder
			decide := func(options []Option, raw json.RawMessage) (string, bool) {
				return "reject-once", false
			}
			if _, err := readProviderLoop(bufio.NewReader(pr), &out, "", decide, tc.kind, nil); err != nil {
				t.Fatal(err)
			}
			line, _, _ := strings.Cut(out.String(), "\n")
			var reply struct {
				ID json.RawMessage `json:"id"`
			}
			if err := json.Unmarshal([]byte(line), &reply); err != nil {
				t.Fatalf("decode reply %q: %v", line, err)
			}
			if string(reply.ID) != "7" {
				t.Fatalf("permission reply id = %s, want numeric 7 echoed unchanged", reply.ID)
			}
		})
	}
}

func TestCodexNetworkApprovalDoesNotInheritExecuteGrant(t *testing.T) {
	var out strings.Builder
	called := false
	event, err := answerCodexRequest(&out, map[string]any{"id": 7, "method": "item/commandExecution/requestApproval", "params": map[string]any{"networkApprovalContext": map[string]string{"host": "example.com", "protocol": "https"}}}, func([]Option, json.RawMessage) (string, bool) { called = true; return "accept", true })
	if err != nil {
		t.Fatal(err)
	}
	var reply struct {
		Result struct {
			Decision string `json:"decision"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out.String()), &reply); err != nil {
		t.Fatal(err)
	}
	if called || event.OptionID != "decline" || reply.Result.Decision != "decline" {
		t.Fatal("network approval inherited execution grant")
	}
}

// A tool-server call is decided as mcp__<server>__<tool> on the exact
// arguments Codex will run; anything unreadable or unrelated is declined
// without asking the fence and without failing the turn (#750).
func TestCodexToolElicitationGoesThroughTheFence(t *testing.T) {
	toolCall := func(args any) map[string]any {
		meta := map[string]any{"codex_approval_kind": "mcp_tool_call", "tool_name": "write", "tool_params": args}
		return map[string]any{"serverName": "docs", "threadId": "t", "mode": "form", "message": "allow?", "_meta": meta, "requestedSchema": map[string]any{}}
	}
	cases := []struct {
		name       string
		params     map[string]any
		allow      bool
		wantAsked  bool
		wantAction string
	}{
		{"allowed call", toolCall(map[string]any{"path": "a"}), true, true, "accept"},
		{"denied call", toolCall(map[string]any{"path": "a"}), false, true, "decline"},
		{"unreadable arguments", toolCall("path=a"), true, false, "decline"},
		{"server form", map[string]any{"serverName": "docs", "threadId": "t", "mode": "form", "message": "your name?", "requestedSchema": map[string]any{}}, true, false, "decline"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var asked struct {
				ToolName string         `json:"toolName"`
				RawInput map[string]any `json:"rawInput"`
			}
			called := false
			var out strings.Builder
			ev, err := answerCodexRequest(&out, map[string]any{"id": 9, "method": "mcpServer/elicitation/request", "params": tc.params}, func(options []Option, raw json.RawMessage) (string, bool) {
				called = true
				_ = json.Unmarshal(raw, &asked)
				return options[0].ID, tc.allow
			})
			if err != nil {
				t.Fatal(err)
			}
			var reply struct {
				Result map[string]any `json:"result"`
			}
			if err := json.Unmarshal([]byte(out.String()), &reply); err != nil {
				t.Fatal(err)
			}
			// Only an action: no persist marker, so an accept covers this call alone.
			if called != tc.wantAsked || ev.OptionID != tc.wantAction || len(reply.Result) != 1 || reply.Result["action"] != tc.wantAction {
				t.Fatalf("asked %v, event %q, reply %v", called, ev.OptionID, reply.Result)
			}
			if tc.wantAsked && (asked.ToolName != "mcp__docs__write" || asked.RawInput["path"] != "a" || len(asked.RawInput) != 1) {
				t.Fatalf("decided %+v", asked)
			}
		})
	}
}

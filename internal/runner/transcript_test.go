package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/guest"
	"github.com/sannrox/rusui/internal/provider"
)

// Protocol envelopes and raw tool output must never become transcript text.
// The same message/call/result fixture traverses each real host parser.
func TestHostProtocolsNormalizeSameTranscript(t *testing.T) {
	const secret = "fixture-turn-token-123456789"
	for _, protocol := range []string{guest.ProtocolACP, guest.ProtocolClaude, guest.ProtocolCodex} {
		t.Run(protocol, func(t *testing.T) {
			input, guestOut := io.Pipe()
			guestIn, output := io.Pipe()
			t.Cleanup(func() { _ = input.Close(); _ = guestOut.Close(); _ = guestIn.Close(); _ = output.Close() })
			go func() {
				scan := bufio.NewScanner(guestIn)
				send := func(v any) { raw, _ := json.Marshal(v); _, _ = fmt.Fprintln(guestOut, string(raw)) }
				for scan.Scan() {
					var msg map[string]any
					if json.Unmarshal(scan.Bytes(), &msg) != nil {
						return
					}
					method, _ := msg["method"].(string)
					switch method {
					case "initialize":
						result := map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}
						if protocol == guest.ProtocolCodex {
							result = map[string]any{"userAgent": "codex-fixture"}
						}
						send(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": result})
						continue
					case "initialized":
						continue
					case "session/new":
						send(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": map[string]any{"sessionId": "fixture"}})
						continue
					case "thread/start":
						send(map[string]any{"id": msg["id"], "result": map[string]any{"thread": map[string]any{"id": "fixture"}}})
						continue
					case "account/login/start":
						send(map[string]any{"id": msg["id"], "result": map[string]any{"type": "apiKey"}})
						continue
					case "session/prompt", "turn/start":
					default:
						if protocol != guest.ProtocolClaude {
							continue
						}
						send(map[string]any{"type": "system", "subtype": "init", "session_id": "fixture", "claude_code_version": provider.ClaudeCodeVersion})
					}
					switch protocol {
					case guest.ProtocolACP:
						for _, update := range []map[string]any{
							{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "hello " + secret}},
							{"sessionUpdate": "tool_call", "toolCallId": "tool-1", "title": "Bash", "kind": "execute", "status": "pending", "rawInput": map[string]any{"command": "echo " + secret, "content": "private-file-body"}},
							{"sessionUpdate": "tool_call_update", "toolCallId": "tool-1", "status": "completed", "content": []any{map[string]any{"text": secret}}, "rawOutput": secret},
						} {
							send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "fixture", "update": update}})
						}
						send(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": map[string]any{"stopReason": "end_turn"}})
					case guest.ProtocolClaude:
						send(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "hello " + secret}, map[string]any{"type": "tool_use", "id": "tool-1", "name": "Bash", "input": map[string]any{"command": "echo " + secret}}}}})
						send(map[string]any{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "tool-1", "content": secret}}}})
						send(map[string]any{"type": "result"})
					case guest.ProtocolCodex:
						send(map[string]any{"method": "item/started", "params": map[string]any{"item": map[string]any{"type": "agentMessage", "id": "message-1", "text": ""}}})
						send(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"delta": "hello " + secret}})
						send(map[string]any{"method": "item/completed", "params": map[string]any{"item": map[string]any{"type": "agentMessage", "id": "message-1", "text": "hello " + secret}}})
						for _, method := range []string{"item/started", "item/completed"} {
							send(map[string]any{"method": method, "params": map[string]any{"item": map[string]any{"type": "commandExecution", "id": "tool-1", "command": "echo " + secret, "status": "completed", "aggregatedOutput": secret}}})
						}
						send(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]any{"status": "completed"}}})
					}
					return
				}
			}()
			log := &receiptLog{}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a := &Assignment{Guest: "fixture", GuestSpec: guest.Entry{Protocol: protocol}, TurnToken: secret}
			if _, err := HostACP(ctx, a, &acp.Client{In: input, Out: output, Rec: log}, t.TempDir()); err != nil {
				t.Fatal(err)
			}
			var updates []string
			for _, row := range log.rows {
				if row.Type == acp.ActionUpdate {
					raw, _ := json.Marshal(row.Body)
					updates = append(updates, string(raw))
				}
			}
			title := "Bash"
			if protocol == guest.ProtocolCodex {
				title = "commandExecution"
			}
			want := []string{
				`{"update":{"content":{"text":"hello [redacted]","type":"text"},"sessionUpdate":"agent_message_chunk"}}`,
				fmt.Sprintf(`{"update":{"kind":"execute","rawInput":{"command":"echo [redacted]"},"sessionUpdate":"tool_call","status":"pending","title":%q,"toolCallId":"tool-1"}}`, title),
				`{"update":{"sessionUpdate":"tool_call_update","status":"completed","toolCallId":"tool-1"}}`,
			}
			if strings.Join(updates, "\n") != strings.Join(want, "\n") {
				t.Fatalf("got %s\nwant %s", strings.Join(updates, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

// Chunking cannot bypass redaction or retain a secret prefix at the cap.
// Authority inputs remain exact because the plane revalidates them later.
func TestTranscriptChunkLimitAndAuthorityInputs(t *testing.T) {
	const token = "chunked-fixture-token-123456789"
	log := &receiptLog{}
	rec := newTranscriptRecorder(&Assignment{TurnToken: token}, log)
	message := func(text string) {
		t.Helper()
		_, err := rec.Record(acp.Receipt{Type: acp.ActionUpdate, Reason: acp.ReasonRecorded, Body: acp.SessionUpdateParams{Update: map[string]any{
			"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text},
		}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	message(strings.Repeat("x", 2*transcriptTextCap-5) + token[:5])
	message(token[5:])
	input := json.RawMessage(`{"command":"` + strings.Repeat("x", transcriptTextCap) + `; npm publish","rawInput":{"changes":[{"content":"` + token + `"}]}}`)
	permission := acp.PermissionParams{ToolCall: input, Options: []acp.PermOption{{OptionID: "allow-once"}}}
	for _, typ := range []string{acp.ActionPermission, acp.ActionApproval} {
		if _, err := rec.Record(acp.Receipt{Type: typ, Body: permission}); err != nil {
			t.Fatal(err)
		}
	}
	rec.Flush()
	if len(log.rows) != 3 {
		t.Fatalf("receipts %d", len(log.rows))
	}
	first, _ := json.Marshal(log.rows[0].Body)
	if string(first) != `{"update":{"content":{"text":"[message exceeds transcript limit]","type":"text"},"sessionUpdate":"agent_message_chunk"}}` {
		t.Fatalf("oversized message retained raw prefix: %s", first)
	}
	want, _ := json.Marshal(permission)
	for _, row := range log.rows[1:] {
		got, _ := json.Marshal(row.Body)
		if string(got) != string(want) {
			t.Fatal("permission input changed before revalidation")
		}
	}
}

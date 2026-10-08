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

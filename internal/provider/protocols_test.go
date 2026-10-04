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
	res, err := readProviderLoop(bufio.NewReader(pr), io.Discard, "sess-1", nil, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if res.Cursor != "sess-1" {
		t.Fatalf("cursor %q", res.Cursor)
	}
	if len(res.Events) != 0 {
		t.Fatalf("claude host keeps Cursor only, events %+v", res.Events)
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
		{"codex", "item/permission"},
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
			if _, err := readProviderLoop(bufio.NewReader(pr), &out, "", decide, tc.kind); err != nil {
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

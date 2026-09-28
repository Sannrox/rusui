package provider

import (
	"bufio"
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

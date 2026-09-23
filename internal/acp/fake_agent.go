package acp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
)

// FakeAgent is a stdio ACP guest used by the conformance harness. It
// exercises initialize, session/new, session/load, session/prompt,
// session/update, every fs/* and terminal/* method, and
// session/request_permission.
type FakeAgent struct {
	In  io.Reader
	Out io.Writer
	// ToolCall is the permission request's tool call; empty sends a bare id.
	ToolCall json.RawMessage
	// PermissionOption receives the option the client selected.
	PermissionOption func(string)
	// SessionCwd receives the cwd of session/new.
	SessionCwd func(string)

	mu      sync.Mutex
	pending map[string]chan rpcMessage
	seq     atomic.Int64
	writes  sync.Mutex
}

func (a *FakeAgent) Run() error {
	a.pending = map[string]chan rpcMessage{}
	sc := bufio.NewScanner(a.In)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var msg rpcMessage
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			return err
		}
		if len(msg.ID) > 0 && msg.Method == "" {
			a.mu.Lock()
			ch := a.pending[string(msg.ID)]
			a.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
			continue
		}
		if err := a.handle(msg); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (a *FakeAgent) handle(msg rpcMessage) error {
	switch msg.Method {
	case MethodInitialize:
		return a.reply(msg.ID, map[string]any{
			"protocolVersion":   1,
			"agentInfo":         map[string]any{"name": "fake-acp", "version": "0"},
			"agentCapabilities": map[string]any{"loadSession": true},
		})
	case MethodSessionNew:
		var p SessionNewParams
		_ = json.Unmarshal(msg.Params, &p)
		if a.SessionCwd != nil {
			a.SessionCwd(p.Cwd)
		}
		if !strings.HasPrefix(p.Cwd, "/") {
			// Real guests (the Claude Code adapter) reject a relative or empty cwd.
			return a.write(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Error: &rpcError{Code: -32602, Message: "Invalid params: `cwd` must be an absolute path"}})
		}
		return a.reply(msg.ID, map[string]any{"sessionId": "sess-fake"})
	case MethodSessionLoad:
		var p SessionLoadParams
		_ = json.Unmarshal(msg.Params, &p)
		if p.SessionID != "sess-fake" {
			return a.write(rpcMessage{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Error:   &rpcError{Code: -32000, Message: "session/load failed"},
			})
		}
		return a.reply(msg.ID, map[string]any{})
	case MethodSessionPrompt:
		go a.promptTurn(msg.ID)
		return nil
	default:
		if len(msg.ID) > 0 {
			return a.write(rpcMessage{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Error:   &rpcError{Code: -32601, Message: "fake agent: " + msg.Method},
			})
		}
		return nil
	}
}

func (a *FakeAgent) promptTurn(id json.RawMessage) {
	_ = a.notify(MethodSessionUpdate, SessionUpdateParams{
		SessionID: "sess-fake",
		Update: map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]any{"type": "text", "text": "pong"},
		},
	})
	_ = a.notify(MethodSessionUpdate, SessionUpdateParams{
		SessionID: "sess-fake",
		Update: map[string]any{
			"sessionUpdate": "tool_call",
			"toolCallId":    "tc-1",
			"title":         "run_terminal_command",
		},
	})
	_ = a.roundTrip(MethodFSReadTextFile, FSReadParams{Path: "/tmp/probe.txt"})
	_ = a.roundTrip(MethodFSWriteTextFile, FSWriteParams{Path: "/tmp/probe.txt", Content: "x"})
	_ = a.roundTrip(MethodTerminalCreate, TerminalCreateParams{Command: "true", Cwd: "/tmp"})
	_ = a.roundTrip(MethodTerminalOutput, TerminalRef{TerminalID: "term-recorded"})
	_ = a.roundTrip(MethodTerminalWaitForExit, TerminalRef{TerminalID: "term-recorded"})
	_ = a.roundTrip(MethodTerminalKill, TerminalRef{TerminalID: "term-recorded"})
	_ = a.roundTrip(MethodTerminalRelease, TerminalRef{TerminalID: "term-recorded"})
	toolCall := a.ToolCall
	if len(toolCall) == 0 {
		toolCall = json.RawMessage(`{"toolCallId":"tc-1"}`)
	}
	_ = a.roundTrip(MethodRequestPermission, PermissionParams{
		SessionID: "sess-fake",
		ToolCall:  toolCall,
		Options: []PermOption{
			{OptionID: "allow-once", Name: "Allow", Kind: "allow_once"},
			{OptionID: "reject-once", Name: "Reject", Kind: "reject_once"},
		},
	})
	_ = a.roundTrip("evil/bypass", map[string]string{"secret": "no"})
	_ = a.reply(id, PromptResult{StopReason: "end_turn"})
}

func (a *FakeAgent) roundTrip(method string, params any) error {
	idNum := a.seq.Add(1)
	id, _ := json.Marshal(idNum)
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	ch := make(chan rpcMessage, 1)
	a.mu.Lock()
	a.pending[string(id)] = ch
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.pending, string(id))
		a.mu.Unlock()
	}()
	if err := a.write(rpcMessage{JSONRPC: "2.0", ID: id, Method: method, Params: raw}); err != nil {
		return err
	}
	msg := <-ch
	if string(msg.ID) != string(id) {
		return fmt.Errorf("fake agent: expected id %s got %s", id, msg.ID)
	}
	if method == MethodRequestPermission && a.PermissionOption != nil {
		var out PermissionOutcome
		if json.Unmarshal(msg.Result, &out) == nil {
			a.PermissionOption(out.Outcome.OptionID)
		}
	}
	return nil
}

func (a *FakeAgent) notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return a.write(rpcMessage{JSONRPC: "2.0", Method: method, Params: raw})
}

func (a *FakeAgent) reply(id json.RawMessage, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return a.write(rpcMessage{JSONRPC: "2.0", ID: id, Result: raw})
}

func (a *FakeAgent) write(msg rpcMessage) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	a.writes.Lock()
	defer a.writes.Unlock()
	_, err = fmt.Fprintf(a.Out, "%s\n", b)
	return err
}

package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
)

// Client is the ACP host. The guest agent is a subprocess; this type
// owns stdio JSON-RPC, receipts, and permission answers. It does not
// embed a model loop.
type Client struct {
	In   io.Reader
	Out  io.Writer
	Rec  Recorder
	Perm PermissionGate
	Wait UnmatchedWaiter
	Ctx  context.Context

	mu           sync.Mutex
	pending      map[string]chan rpcMessage
	seq          atomic.Int64
	once         sync.Once
	writes       sync.Mutex
	promptMu     sync.Mutex
	promptCtx    context.Context
	promptCancel context.CancelFunc
	err          atomic.Value
}

// UnmatchedWaiter waits on a live unmatched permission RPC.
type UnmatchedWaiter func(ctx context.Context, p PermissionParams) Decision

func (c *Client) start() {
	c.once.Do(func() {
		c.pending = map[string]chan rpcMessage{}
		go c.readLoop()
	})
}

func (c *Client) readLoop() {
	sc := bufio.NewScanner(c.In)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			c.err.Store(fmt.Errorf("acp: decode: %w", err))
			continue
		}
		if len(msg.ID) > 0 && msg.Method == "" {
			c.mu.Lock()
			ch := c.pending[string(msg.ID)]
			c.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
			continue
		}
		if msg.Method != "" {
			c.handleInbound(msg)
		}
	}
	if err := sc.Err(); err != nil {
		c.err.Store(fmt.Errorf("acp: read: %w", err))
	}
}

func (c *Client) handleInbound(msg rpcMessage) {
	switch msg.Method {
	case MethodSessionUpdate:
		c.recordUpdate(msg.Params)
	case MethodRequestPermission:
		c.answerPermission(msg)
	case MethodFSReadTextFile:
		c.answerFSRead(msg)
	case MethodFSWriteTextFile:
		c.answerFSWrite(msg)
	case MethodTerminalCreate:
		c.answerTerminalCreate(msg)
	case MethodTerminalOutput, MethodTerminalWaitForExit, MethodTerminalKill, MethodTerminalRelease:
		c.answerTerminal(msg)
	default:
		_ = c.record(Receipt{Type: ActionUnknown, Reason: ReasonRecorded, Body: map[string]any{"method": msg.Method, "params": jsonRaw(msg.Params)}})
		if len(msg.ID) > 0 {
			_ = c.write(rpcMessage{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Error:   &rpcError{Code: -32601, Message: "method not handled by rusui ACP client"},
			})
		}
	}
}

func (c *Client) recordUpdate(params json.RawMessage) {
	var p SessionUpdateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	kind, _ := p.Update["sessionUpdate"].(string)
	if kind != "tool_call" && kind != "tool_call_update" {
		return
	}
	_ = c.record(Receipt{Type: ActionUpdate, Reason: ReasonRecorded, Body: p})
}

func (c *Client) answerPermission(msg rpcMessage) {
	var p PermissionParams
	_ = json.Unmarshal(msg.Params, &p)
	_ = c.record(Receipt{Type: ActionPermission, Reason: ReasonRecorded, Body: p})
	gate := c.Perm
	if gate == nil {
		gate = DenyUnmatched{}
	}
	d := gate.Decide(p)
	cancelled := false
	if !d.Matched {
		_ = c.record(Receipt{Type: ActionApproval, Reason: ReasonUnmatched, Body: p})
		if c.Wait != nil {
			waitCtx := c.permissionWaitContext()
			d = c.Wait(waitCtx, p)
			cancelled = waitCtx.Err() != nil
			if cancelled {
				d = Decision{}
			}
		}
	} else if !d.Allow {
		_ = c.record(Receipt{Type: ActionApproval, Reason: ReasonDenied, Body: p})
	}
	allow := d.Matched && d.Allow
	option := pickOption(p.Options, allow)
	if allow && strings.Contains(strings.ToLower(option), "always") {
		option = "allow-once"
	}
	permissionResult := PermissionOutcomeValue{Outcome: "selected", OptionID: option}
	if cancelled || c.permissionWaitContext().Err() != nil {
		permissionResult = PermissionOutcomeValue{Outcome: "cancelled"}
	}
	out := PermissionOutcome{Outcome: permissionResult}
	raw, _ := json.Marshal(out)
	_ = c.write(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: raw})
}

func (c *Client) answerFSRead(msg rpcMessage) {
	var p FSReadParams
	_ = json.Unmarshal(msg.Params, &p)
	_ = c.record(Receipt{Type: ActionFSRead, Reason: ReasonRecorded, Body: p})
	raw, _ := json.Marshal(FSReadResult{Content: ""})
	_ = c.write(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: raw})
}

func (c *Client) answerFSWrite(msg rpcMessage) {
	var p FSWriteParams
	_ = json.Unmarshal(msg.Params, &p)
	_ = c.record(Receipt{Type: ActionFSWrite, Reason: ReasonDenied, Body: p})
	_ = c.write(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage(`{}`)})
}

func (c *Client) answerTerminalCreate(msg rpcMessage) {
	_ = c.record(Receipt{Type: ActionTerminalCreate, Reason: ReasonRecorded, Body: jsonRaw(msg.Params)})
	raw, _ := json.Marshal(TerminalIDResult{TerminalID: "term-recorded"})
	_ = c.write(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: raw})
}

func (c *Client) answerTerminal(msg rpcMessage) {
	typ := terminalAction(msg.Method)
	_ = c.record(Receipt{Type: typ, Reason: ReasonRecorded, Body: map[string]any{"method": msg.Method, "params": jsonRaw(msg.Params)}})
	var result any
	switch msg.Method {
	case MethodTerminalOutput:
		result = map[string]any{"output": "", "truncated": false}
	case MethodTerminalWaitForExit:
		result = map[string]any{"exitCode": 0, "signal": nil}
	default:
		result = map[string]any{}
	}
	raw, _ := json.Marshal(result)
	_ = c.write(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: raw})
}

func terminalAction(method string) string {
	switch method {
	case MethodTerminalOutput:
		return ActionTerminalOutput
	case MethodTerminalWaitForExit:
		return ActionTerminalWait
	case MethodTerminalKill:
		return ActionTerminalKill
	case MethodTerminalRelease:
		return ActionTerminalRelease
	default:
		return ActionUnknown
	}
}

func (c *Client) record(r Receipt) error {
	if c.Rec == nil {
		return nil
	}
	return c.Rec.Record(r)
}

func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	return c.call(ctx, method, params, result, nil)
}

func (c *Client) call(ctx context.Context, method string, params, result any, submitted func()) error {
	c.start()
	idNum := c.seq.Add(1)
	id, _ := json.Marshal(idNum)
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	ch := make(chan rpcMessage, 1)
	c.mu.Lock()
	c.pending[string(id)] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, string(id))
		c.mu.Unlock()
	}()
	if err := c.write(rpcMessage{JSONRPC: "2.0", ID: id, Method: method, Params: raw}); err != nil {
		return err
	}
	if submitted != nil {
		submitted()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case msg := <-ch:
		if msg.Error != nil {
			return fmt.Errorf("acp: %s: %s", method, msg.Error.Message)
		}
		if result != nil && len(msg.Result) > 0 {
			return json.Unmarshal(msg.Result, result)
		}
		return nil
	}
}

func (c *Client) Initialize(ctx context.Context) (*InitializeResult, error) {
	var out InitializeResult
	err := c.Call(ctx, MethodInitialize, InitializeParams{
		ProtocolVersion: 1,
		ClientCapabilities: map[string]any{
			"fs":       map[string]any{"readTextFile": true, "writeTextFile": true},
			"terminal": true,
		},
		ClientInfo: map[string]any{"name": "rusui", "title": "rusui", "version": "0"},
	}, &out)
	return &out, err
}

func (c *Client) SessionNew(ctx context.Context, cwd string) (string, error) {
	var out SessionIDResult
	err := c.Call(ctx, MethodSessionNew, SessionNewParams{Cwd: cwd, MCPServers: []any{}}, &out)
	return out.SessionID, err
}

func (c *Client) SessionLoad(ctx context.Context, sessionID, cwd string) error {
	return c.Call(ctx, MethodSessionLoad, SessionLoadParams{SessionID: sessionID, Cwd: cwd}, nil)
}

func (c *Client) SessionPrompt(ctx context.Context, sessionID, text string) (*PromptResult, error) {
	return c.SessionPromptSubmitted(ctx, sessionID, text, nil)
}

// SessionPromptSubmitted closes submitted after the session/prompt request is written.
func (c *Client) SessionPromptSubmitted(ctx context.Context, sessionID, text string, submitted chan<- struct{}) (*PromptResult, error) {
	permissionParent := c.Ctx
	if permissionParent == nil {
		permissionParent = ctx
	}
	permissionCtx, cancel := context.WithCancel(permissionParent)
	stopOuter := context.AfterFunc(ctx, cancel)
	c.promptMu.Lock()
	previousCancel := c.promptCancel
	c.promptCtx = permissionCtx
	c.promptCancel = cancel
	c.promptMu.Unlock()
	if previousCancel != nil {
		previousCancel()
	}
	defer func() {
		stopOuter()
		cancel()
		c.promptMu.Lock()
		if c.promptCtx == permissionCtx {
			c.promptCtx = nil
			c.promptCancel = nil
		}
		c.promptMu.Unlock()
	}()
	var out PromptResult
	var signal func()
	if submitted != nil {
		signal = func() { close(submitted) }
	}
	err := c.call(ctx, MethodSessionPrompt, PromptParams{
		SessionID: sessionID,
		Prompt:    []PromptBlock{{Type: "text", Text: text}},
	}, &out, signal)
	return &out, err
}

func (c *Client) SessionCancel(sessionID string) error {
	c.promptMu.Lock()
	cancel := c.promptCancel
	c.promptMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return c.Notify(MethodSessionCancel, SessionCancelParams{SessionID: sessionID})
}

func (c *Client) permissionWaitContext() context.Context {
	c.promptMu.Lock()
	ctx := c.promptCtx
	c.promptMu.Unlock()
	if ctx == nil {
		ctx = c.Ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return ctx
}

func (c *Client) Notify(method string, params any) error {
	c.start()
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.write(rpcMessage{JSONRPC: "2.0", Method: method, Params: raw})
}

func (c *Client) write(msg rpcMessage) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writes.Lock()
	defer c.writes.Unlock()
	_, err = fmt.Fprintf(c.Out, "%s\n", b)
	return err
}

func jsonRaw(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return string(b)
	}
	return v
}

package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// EditorPlane is the plane HTTP/SSE API as used by the editor shim.
// The shim holds no guest model loop and no separate session store.
type EditorPlane interface {
	GetSession(ctx context.Context, id int64) (*EditorSession, error)
	FollowUp(ctx context.Context, id int64, prompt string) error
	Cancel(ctx context.Context, id int64) error
	Attach(ctx context.Context, id int64) (<-chan EditorUpdate, error)
	PendingApprovals(ctx context.Context, sessionID int64) ([]EditorApproval, error)
	Decide(ctx context.Context, actionID, decision string) error
}

type EditorSession struct {
	ID     int64  `json:"id"`
	State  string `json:"state"`
	Env    string `json:"environment_state,omitempty"`
	Prompt string `json:"prompt,omitempty"`
}

type EditorUpdate struct {
	Kind string
	Body json.RawMessage
}

type EditorApproval struct {
	ID     string
	Body   string
	Reason string
}

// EditorAgent is an ACP *agent* over stdio. An editor is the ACP client.
// session/new is rejected: load an existing rusui session instead.
type EditorAgent struct {
	In    io.Reader
	Out   io.Writer
	Plane EditorPlane

	mu      sync.Mutex
	loaded  int64
	pending map[string]chan rpcMessage
	seq     atomic.Int64
	writes  sync.Mutex
}

func (a *EditorAgent) Serve(ctx context.Context) error {
	a.pending = map[string]chan rpcMessage{}
	sc := bufio.NewScanner(a.In)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
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
		if msg.Method != "" {
			a.handle(ctx, msg)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	return sc.Err()
}

func (a *EditorAgent) handle(ctx context.Context, msg rpcMessage) {
	switch msg.Method {
	case MethodInitialize:
		a.handleInitialize(msg)
	case MethodSessionLoad:
		a.handleLoad(ctx, msg)
	case MethodSessionPrompt:
		a.handlePrompt(ctx, msg)
	case MethodSessionCancel:
		a.handleCancel(ctx, msg)
	case MethodSessionNew:
		a.replyErr(msg.ID, -32601, "load an existing rusui session; session/new is unsupported")
	default:
		a.replyErr(msg.ID, -32601, "unsupported method "+msg.Method)
	}
}

func (a *EditorAgent) handleInitialize(msg rpcMessage) {
	var p InitializeParams
	_ = json.Unmarshal(msg.Params, &p)
	if p.ProtocolVersion != 0 && p.ProtocolVersion != ProtocolVersion {
		a.replyErr(msg.ID, -32602, "unsupported protocolVersion")
		return
	}
	a.reply(msg.ID, InitializeResult{
		ProtocolVersion: ProtocolVersion,
		AgentInfo: map[string]any{
			"name":    EditorAgentName,
			"version": "1",
			"title":   "rusui ACP",
		},
	})
}

func (a *EditorAgent) handleLoad(ctx context.Context, msg rpcMessage) {
	var p SessionLoadParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		a.replyErr(msg.ID, -32602, "invalid params")
		return
	}
	id, err := strconv.ParseInt(p.SessionID, 10, 64)
	if err != nil {
		a.replyErr(msg.ID, -32602, "sessionId must be a rusui session id")
		return
	}
	sess, err := a.Plane.GetSession(ctx, id)
	if err != nil {
		a.replyErr(msg.ID, -32000, err.Error())
		return
	}
	if sess.Env == "expired" {
		a.replyErr(msg.ID, -32000, "environment expired")
		return
	}
	a.mu.Lock()
	a.loaded = id
	a.mu.Unlock()
	a.reply(msg.ID, SessionIDResult{SessionID: p.SessionID})
	go a.stream(ctx, id)
	go a.permissions(ctx, id)
}

func (a *EditorAgent) handlePrompt(ctx context.Context, msg rpcMessage) {
	id := a.loadedID()
	if id == 0 {
		a.replyErr(msg.ID, -32000, "no session loaded")
		return
	}
	var p PromptParams
	_ = json.Unmarshal(msg.Params, &p)
	var text strings.Builder
	for _, b := range p.Prompt {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	if err := a.Plane.FollowUp(ctx, id, text.String()); err != nil {
		a.replyErr(msg.ID, -32000, err.Error())
		return
	}
	a.reply(msg.ID, PromptResult{StopReason: "end_turn"})
}

func (a *EditorAgent) handleCancel(ctx context.Context, msg rpcMessage) {
	id := a.loadedID()
	if id == 0 {
		a.replyErr(msg.ID, -32000, "no session loaded")
		return
	}
	if err := a.Plane.Cancel(ctx, id); err != nil {
		a.replyErr(msg.ID, -32000, err.Error())
		return
	}
	a.reply(msg.ID, map[string]any{"ok": true})
}

func (a *EditorAgent) stream(ctx context.Context, id int64) {
	ch, err := a.Plane.Attach(ctx, id)
	if err != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case u, ok := <-ch:
			if !ok {
				return
			}
			_ = a.notify(MethodSessionUpdate, SessionUpdateParams{
				SessionID: strconv.FormatInt(id, 10),
				Update:    map[string]any{"kind": u.Kind, "body": u.Body},
			})
		}
	}
}

func (a *EditorAgent) permissions(ctx context.Context, id int64) {
	pending, err := a.Plane.PendingApprovals(ctx, id)
	if err != nil {
		return
	}
	for _, ap := range pending {
		var out PermissionOutcome
		tc := json.RawMessage(ap.Body)
		if !json.Valid(tc) {
			tc = json.RawMessage(`{}`)
		}
		err := a.call(ctx, MethodRequestPermission, PermissionParams{
			SessionID: strconv.FormatInt(id, 10),
			ToolCall:  tc,
			Options: []PermOption{
				{OptionID: "allow", Name: "Allow", Kind: "allow_once"},
				{OptionID: "deny", Name: "Deny", Kind: "reject_once"},
			},
		}, &out)
		if err != nil {
			continue
		}
		dec := "deny"
		if out.Outcome.OptionID == "allow" {
			dec = "allow"
		}
		_ = a.Plane.Decide(ctx, ap.ID, dec)
	}
}

func (a *EditorAgent) loadedID() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.loaded
}

func (a *EditorAgent) reply(id json.RawMessage, result any) {
	raw, _ := json.Marshal(result)
	_ = a.write(rpcMessage{JSONRPC: "2.0", ID: id, Result: raw})
}

func (a *EditorAgent) replyErr(id json.RawMessage, code int, msg string) {
	_ = a.write(rpcMessage{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func (a *EditorAgent) notify(method string, params any) error {
	raw, _ := json.Marshal(params)
	return a.write(rpcMessage{JSONRPC: "2.0", Method: method, Params: raw})
}

func (a *EditorAgent) call(ctx context.Context, method string, params, result any) error {
	raw, _ := json.Marshal(params)
	idNum := a.seq.Add(1)
	id, _ := json.Marshal(idNum)
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
	select {
	case <-ctx.Done():
		return ctx.Err()
	case msg := <-ch:
		if msg.Error != nil {
			return fmt.Errorf("acp: %s", msg.Error.Message)
		}
		if result != nil && len(msg.Result) > 0 {
			return json.Unmarshal(msg.Result, result)
		}
		return nil
	}
}

func (a *EditorAgent) write(msg rpcMessage) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	a.writes.Lock()
	defer a.writes.Unlock()
	_, err = a.Out.Write(append(b, '\n'))
	return err
}

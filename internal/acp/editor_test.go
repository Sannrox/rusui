package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakePlane struct {
	mu       sync.Mutex
	sess     *EditorSession
	err      error
	follows  []string
	canceled bool
	started  bool
	updates  chan EditorUpdate
	pending  []EditorApproval
	decided  []string
}

func (f *fakePlane) GetSession(ctx context.Context, id int64) (*EditorSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if f.sess == nil || f.sess.ID != id {
		return nil, fmt.Errorf("not found")
	}
	s := *f.sess
	return &s, nil
}

func (f *fakePlane) FollowUp(ctx context.Context, id int64, prompt string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.follows = append(f.follows, prompt)
	return f.err
}

func (f *fakePlane) Cancel(ctx context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.canceled = true
	return f.err
}

func (f *fakePlane) Attach(ctx context.Context, id int64) (<-chan EditorUpdate, error) {
	if f.updates == nil {
		ch := make(chan EditorUpdate)
		close(ch)
		return ch, nil
	}
	return f.updates, nil
}

func (f *fakePlane) PendingApprovals(ctx context.Context, sessionID int64) ([]EditorApproval, error) {
	return f.pending, nil
}

func (f *fakePlane) Decide(ctx context.Context, actionID, decision string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decided = append(f.decided, actionID+":"+decision)
	return nil
}

func writeRPC(t *testing.T, w io.Writer, id int, method string, params any) {
	t.Helper()
	raw, _ := json.Marshal(params)
	jid, _ := json.Marshal(id)
	b, _ := json.Marshal(rpcMessage{JSONRPC: "2.0", ID: jid, Method: method, Params: raw})
	if _, err := w.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

func readReply(t *testing.T, r io.Reader) rpcMessage {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1)
	for time.Now().Before(deadline) {
		n, err := r.Read(tmp)
		if n == 1 {
			if tmp[0] == '\n' {
				var msg rpcMessage
				if err := json.Unmarshal(buf, &msg); err != nil {
					t.Fatal(err)
				}
				if msg.Method != "" {
					buf = buf[:0]
					continue
				}
				return msg
			}
			buf = append(buf, tmp[0])
			continue
		}
		if err != nil && err != io.EOF {
			time.Sleep(2 * time.Millisecond)
		}
	}
	t.Fatal("timeout")
	return rpcMessage{}
}

func TestEditorAgentLoadPromptCancel(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	f := &fakePlane{sess: &EditorSession{ID: 7, State: "open"}}
	a := &EditorAgent{In: inR, Out: outW, Plane: f}
	go func() { _ = a.Serve(t.Context()) }()

	writeRPC(t, inW, 1, MethodInitialize, InitializeParams{ProtocolVersion: ProtocolVersion})
	msg := readReply(t, outR)
	var init InitializeResult
	if err := json.Unmarshal(msg.Result, &init); err != nil || init.ProtocolVersion != ProtocolVersion || init.AgentInfo["name"] != EditorAgentName {
		t.Fatalf("%+v %s", init, msg.Result)
	}

	writeRPC(t, inW, 2, MethodSessionNew, SessionNewParams{Cwd: "/"})
	msg = readReply(t, outR)
	if msg.Error == nil || !strings.Contains(msg.Error.Message, "session/new") {
		t.Fatalf("session/new %v", msg.Error)
	}
	if f.started {
		t.Fatal("silently started a session")
	}

	writeRPC(t, inW, 3, MethodSessionLoad, SessionLoadParams{SessionID: "7"})
	msg = readReply(t, outR)
	if msg.Error != nil {
		t.Fatal(msg.Error.Message)
	}

	writeRPC(t, inW, 4, MethodSessionPrompt, PromptParams{SessionID: "7", Prompt: []PromptBlock{{Type: "text", Text: "next"}}})
	msg = readReply(t, outR)
	if msg.Error != nil {
		t.Fatal(msg.Error.Message)
	}
	f.mu.Lock()
	got := append([]string(nil), f.follows...)
	f.mu.Unlock()
	if len(got) != 1 || got[0] != "next" {
		t.Fatalf("follow %+v", got)
	}

	writeRPC(t, inW, 5, MethodFSReadTextFile, FSReadParams{Path: "x"})
	msg = readReply(t, outR)
	if msg.Error == nil || !strings.Contains(msg.Error.Message, "unsupported") {
		t.Fatalf("fs %v", msg.Error)
	}

	writeRPC(t, inW, 6, MethodSessionCancel, map[string]any{})
	msg = readReply(t, outR)
	if msg.Error != nil {
		t.Fatal(msg.Error.Message)
	}
	if !f.canceled {
		t.Fatal("cancel")
	}
	_ = inW.Close()
}

func TestEditorAgentAuthAndExpired(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	f := &fakePlane{err: fmt.Errorf("auth")}
	a := &EditorAgent{In: inR, Out: outW, Plane: f}
	go func() { _ = a.Serve(t.Context()) }()
	writeRPC(t, inW, 1, MethodSessionLoad, SessionLoadParams{SessionID: "1"})
	msg := readReply(t, outR)
	if msg.Error == nil || !strings.Contains(msg.Error.Message, "auth") {
		t.Fatalf("%v", msg.Error)
	}
	_ = inW.Close()

	inR, inW = io.Pipe()
	outR, outW = io.Pipe()
	f = &fakePlane{sess: &EditorSession{ID: 1, Env: "expired"}}
	a = &EditorAgent{In: inR, Out: outW, Plane: f}
	go func() { _ = a.Serve(t.Context()) }()
	writeRPC(t, inW, 1, MethodSessionLoad, SessionLoadParams{SessionID: "1"})
	msg = readReply(t, outR)
	if msg.Error == nil || !strings.Contains(msg.Error.Message, "expired") {
		t.Fatalf("expired %v", msg.Error)
	}
	_ = inW.Close()
}

func TestEditorAgentPermissionDecision(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	f := &fakePlane{
		sess:    &EditorSession{ID: 3, State: "open"},
		pending: []EditorApproval{{ID: "act-1", Body: `{"tool":"shell"}`}},
	}
	a := &EditorAgent{In: inR, Out: outW, Plane: f}
	go func() { _ = a.Serve(t.Context()) }()

	raw, _ := json.Marshal(SessionLoadParams{SessionID: "3"})
	id, _ := json.Marshal(1)
	b, _ := json.Marshal(rpcMessage{JSONRPC: "2.0", ID: id, Method: MethodSessionLoad, Params: raw})
	_, _ = inW.Write(append(b, '\n'))

	sc := bufioScan{r: outR}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		line, err := sc.line()
		if err != nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		var msg rpcMessage
		if json.Unmarshal(line, &msg) != nil {
			continue
		}
		if msg.Method == MethodRequestPermission && len(msg.ID) > 0 {
			res, _ := json.Marshal(PermissionOutcome{Outcome: PermissionSelected{Outcome: "selected", OptionID: "deny"}})
			rb, _ := json.Marshal(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: res})
			_, _ = inW.Write(append(rb, '\n'))
			break
		}
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		n := len(f.decided)
		f.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.mu.Lock()
	got := append([]string(nil), f.decided...)
	f.mu.Unlock()
	if len(got) != 1 || got[0] != "act-1:deny" {
		t.Fatalf("decided %v", got)
	}
	_ = inW.Close()
}

type bufioScan struct{ r io.Reader }

func (b bufioScan) line() ([]byte, error) {
	var out []byte
	tmp := make([]byte, 1)
	for {
		n, err := b.r.Read(tmp)
		if n == 1 {
			if tmp[0] == '\n' {
				return out, nil
			}
			out = append(out, tmp[0])
			continue
		}
		if err != nil {
			return out, err
		}
	}
}

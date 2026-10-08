package runner

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/guest"
	"github.com/sannrox/rusui/internal/provider"
	"github.com/sannrox/rusui/internal/store"
)

func TestShikigamiPromptCapabilitiesRecordedAndEnforced(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(map[bool]string{false: "old guest", true: "HTTP guest"}[supported], func(t *testing.T) {
			clientIn, agentOut := io.Pipe()
			agentIn, clientOut := io.Pipe()
			t.Cleanup(func() { _ = clientIn.Close(); _ = clientOut.Close(); _ = agentIn.Close(); _ = agentOut.Close() })
			caps := &acp.PromptCapabilities{Image: supported, EmbeddedContext: supported}
			prompts := make(chan acp.PromptParams, 1)
			go func() {
				_ = (&acp.FakeAgent{In: agentIn, Out: agentOut, PromptCapabilities: caps, PromptStarted: prompts}).Run()
			}()
			st, err := store.Open(filepath.Join(t.TempDir(), "receipts.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = st.Close() }()
			sid := int64(1)
			host := &acp.Client{In: clientIn, Out: clientOut, Rec: acp.StoreRecorder{Store: st, SessionID: &sid}, Perm: acp.DenyUnmatched{}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err = HostACP(ctx, &Assignment{Guest: acp.GuestShikigami, Input: json.RawMessage(`{"body":"inspect"}`), Attachments: []PromptAttachment{
				{Name: "image.png", MIME: "image/png", Data: "iVBORw0KGgo="},
				{Name: "document.pdf", MIME: "application/pdf", Data: base64.StdEncoding.EncodeToString([]byte("%PDF-1.1"))},
			}}, host, t.TempDir())
			if supported && err != nil {
				t.Fatal(err)
			}
			if !supported && (err == nil || !strings.Contains(err.Error(), "guest does not support attachment")) {
				t.Fatalf("old guest: %v", err)
			}
			body, err := store.LatestActionBody(st, sid, acp.ActionInitialize)
			if err != nil {
				t.Fatal(err)
			}
			var observed acp.GuestInitialize
			if err := json.Unmarshal([]byte(body), &observed); err != nil {
				t.Fatal(err)
			}
			if observed.Guest != acp.GuestShikigami || observed.Result.AgentCapabilities.PromptCapabilities != *caps {
				t.Fatalf("initialize receipt %s", body)
			}
			select {
			case p := <-prompts:
				if !supported {
					t.Fatal("unsupported guest received prompt")
				}
				if len(p.Prompt) != 3 || p.Prompt[1].Type != "image" || p.Prompt[2].Resource == nil || p.Prompt[2].Resource.MimeType != "application/pdf" {
					t.Fatalf("prompt %+v", p)
				}
			default:
				if supported {
					t.Fatal("supported guest received no prompt")
				}
			}
		})
	}
}

func stallAfterSession(in io.Reader, out io.Writer) {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	reply := func(id any, result any) {
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
		_, _ = out.Write(append(raw, '\n'))
	}
	for sc.Scan() {
		var msg struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			return
		}
		switch msg.Method {
		case "initialize":
			reply(msg.ID, map[string]any{
				"protocolVersion":   1,
				"agentInfo":         map[string]any{"name": "stall", "version": "0"},
				"agentCapabilities": map[string]any{"loadSession": true},
			})
		case "session/new":
			reply(msg.ID, map[string]any{"sessionId": "sess-stall"})
			return
		}
	}
}

func TestHostACPForwardsSessionMode(t *testing.T) {
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	var got string
	go func() {
		_ = (&acp.FakeAgent{In: agentIn, Out: agentOut, SessionMode: func(mode string) {
			got = mode
		}, PromptHandler: func(acp.PromptParams, <-chan struct{}, func(string, any) error) (acp.PromptResult, error) {
			return acp.PromptResult{StopReason: "end_turn"}, nil
		}}).Run()
	}()
	host := &acp.Client{In: clientIn, Out: clientOut, Perm: acp.DenyUnmatched{}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	in, _ := json.Marshal(map[string]string{"body": "do the thing"})
	_, err := HostACP(ctx, &Assignment{Input: in, Repo: "example/test-repo", Item: 1, ItemKind: "issue", Mode: "ultra"}, host, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got != "ultra" {
		t.Fatalf("session/new mode %q", got)
	}
}

func TestHostACPReturnsWhenGuestStopsReading(t *testing.T) {
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	go stallAfterSession(agentIn, agentOut)
	host := &acp.Client{In: clientIn, Out: clientOut, Perm: acp.DenyUnmatched{}, Wait: func(context.Context, string, acp.PermissionParams) acp.Decision {
		return acp.Decision{}
	}}
	deadline := time.Now().Add(200 * time.Millisecond)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	started := time.Now()
	in, _ := json.Marshal(map[string]string{"body": "do the thing"})
	_, err := HostACP(ctx, &Assignment{Input: in, Repo: "example/test-repo", Item: 1, ItemKind: "issue"}, host, t.TempDir())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("deadline was not observed without an external signal: %s", time.Since(started))
	}
}

func TestHostACPClaudeReadsStreamJSONInit(t *testing.T) {
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	var (
		mu   sync.Mutex
		seen []map[string]any
	)
	go func() {
		var raw []byte
		sc := bufio.NewScanner(agentIn)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for first := true; sc.Scan(); first = false {
			if first {
				// Claude Code 2.1.283 emits system/init only after the first input.
				raw, _ = json.Marshal(map[string]any{
					"type":                "system",
					"subtype":             "init",
					"claude_code_version": provider.ClaudeCodeVersion,
					"session_id":          "claude-sess",
				})
				_, _ = agentOut.Write(append(raw, '\n'))
			}
			var msg map[string]any
			if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
				return
			}
			mu.Lock()
			seen = append(seen, msg)
			mu.Unlock()
			if _, ok := msg["method"]; ok {
				return
			}
			if msg["type"] == "user" {
				raw, _ = json.Marshal(map[string]any{"type": "assistant", "text": "hello-claude"})
				_, _ = agentOut.Write(append(raw, '\n'))
				raw, _ = json.Marshal(map[string]any{"type": "result"})
				_, _ = agentOut.Write(append(raw, '\n'))
				return
			}
		}
	}()
	host := &acp.Client{In: clientIn, Out: clientOut, Perm: acp.DenyUnmatched{}, Wait: func(context.Context, string, acp.PermissionParams) acp.Decision {
		return acp.Decision{}
	}}
	in, _ := json.Marshal(map[string]string{"body": "do the thing"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	art, err := HostACP(ctx, &Assignment{Guest: acp.GuestClaude, Input: in, Repo: "example/test-repo", Item: 1, ItemKind: "run"}, host, t.TempDir())
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if art.GuestSessionID != "claude-sess" {
		t.Fatalf("session %q", art.GuestSessionID)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 {
		t.Fatalf("writes %+v", seen)
	}
	if seen[0]["method"] != nil {
		t.Fatalf("sent ACP method %+v", seen[0])
	}
	if seen[0]["type"] != "user" {
		t.Fatalf("write %+v", seen[0])
	}
	msg, _ := seen[0]["message"].(map[string]any)
	if msg["content"] != "do the thing" {
		t.Fatalf("prompt %+v", seen[0])
	}
}

// Resume is deferred on the Claude host (ADR 0025): a stored cursor does not
// reach the provider, and the new conversation's id becomes the cursor.
func TestHostACPClaudeFollowUpStartsNewConversation(t *testing.T) {
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	go func() {
		sc := bufio.NewScanner(agentIn)
		if sc.Scan() {
			// Claude Code 2.1.283 emits system/init only after the first input.
			raw, _ := json.Marshal(map[string]any{
				"type":                "system",
				"subtype":             "init",
				"claude_code_version": provider.ClaudeCodeVersion,
				"session_id":          "fresh-sess",
			})
			_, _ = agentOut.Write(append(raw, '\n'))
			raw, _ = json.Marshal(map[string]any{"type": "result"})
			_, _ = agentOut.Write(append(raw, '\n'))
		}
	}()
	host := &acp.Client{In: clientIn, Out: clientOut, Perm: acp.DenyUnmatched{}, Wait: func(context.Context, string, acp.PermissionParams) acp.Decision {
		return acp.Decision{}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	a := &Assignment{Guest: acp.GuestClaude, GuestSessionID: "prior-sess", Repo: "example/test-repo", Item: 1, ItemKind: "run"}
	art, err := HostACP(ctx, a, host, t.TempDir())
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if art.GuestSessionID != "fresh-sess" {
		t.Fatalf("session %q", art.GuestSessionID)
	}
	argv, err := acp.SpawnArgsFor(acp.GuestClaude)
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range argv {
		if arg == "--resume" {
			t.Fatalf("claude spawn resumes %v", argv)
		}
	}
}

func TestOneACPTurnFailsWhenGuestStopsReading(t *testing.T) {
	var (
		mu        sync.Mutex
		failCount int
		stopped   atomic.Bool
	)
	deadline := time.Now().UTC().Add(300 * time.Millisecond)
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/runners/hello":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/jobs/claim":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"turn_id":            8,
				"lease_generation":   1,
				"claimed_revision":   1,
				"repo":               "example/test-repo",
				"item":               -8,
				"item_kind":          "run",
				"execution_deadline": deadline,
				"turn_token":         "tok",
				"input":              map[string]string{"body": "implement the package"},
				"driver":             "container",
				"handle":             "guest-8",
			})
		case strings.HasSuffix(r.URL.Path, "/fail"):
			mu.Lock()
			failCount++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"kind": "fail"})
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{})
		}
	}))
	t.Cleanup(hs.Close)

	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	go stallAfterSession(agentIn, agentOut)
	hostFn := func(*Assignment, string) (*acp.Client, func(), error) {
		stop := func() {
			stopped.Store(true)
			_ = clientOut.Close()
			_ = agentIn.Close()
		}
		return &acp.Client{In: clientIn, Out: clientOut, Perm: acp.DenyUnmatched{}, Wait: func(context.Context, string, acp.PermissionParams) acp.Decision {
			return acp.Decision{}
		}}, stop, nil
	}
	cli := &Client{Base: hs.URL, HTTP: hs.Client(), Bootstrap: "wsec", Repo: "example/test-repo", Exec: &env.FakeRuntime{}}
	started := time.Now()
	_, err := OneACPTurnWithOutcome(context.Background(), cli, hostFn)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("deadline was not observed without an external signal: %s", time.Since(started))
	}
	mu.Lock()
	n := failCount
	mu.Unlock()
	if n == 0 {
		t.Fatal("expected fail receipt after deadline")
	}
	if !stopped.Load() {
		t.Fatal("guest stop was not called")
	}
}

func TestHostACPLoadsOrCreatesGuestSession(t *testing.T) {
	t.Parallel()
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	go func() { _ = (&acp.FakeAgent{In: agentIn, Out: agentOut}).Run() }()
	host := &acp.Client{In: clientIn, Out: clientOut, Perm: acp.DenyUnmatched{}, Wait: func(context.Context, string, acp.PermissionParams) acp.Decision {
		return acp.Decision{}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	in, _ := json.Marshal(map[string]string{"title": "ping", "body": "pong"})
	art, err := HostACP(ctx, &Assignment{GuestSessionID: "missing", Input: in, Repo: "example/test-repo", Item: 1, ItemKind: "issue"}, host, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if art.GuestSessionID != "sess-fake" {
		t.Fatalf("restore-failed must session/new, got %q", art.GuestSessionID)
	}
}

func TestHostACPReusesGuestSession(t *testing.T) {
	t.Parallel()
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	go func() { _ = (&acp.FakeAgent{In: agentIn, Out: agentOut}).Run() }()
	host := &acp.Client{In: clientIn, Out: clientOut, Perm: acp.DenyUnmatched{}, Wait: func(context.Context, string, acp.PermissionParams) acp.Decision {
		return acp.Decision{}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	in, _ := json.Marshal(map[string]string{"body": "ping"})
	art, err := HostACP(ctx, &Assignment{GuestSessionID: "sess-fake", Input: in}, host, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if art.GuestSessionID != "sess-fake" {
		t.Fatalf("load kept %q", art.GuestSessionID)
	}
}

func TestHostACPSendsImageAttachment(t *testing.T) {
	t.Parallel()
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	prompts := make(chan acp.PromptParams, 1)
	go func() { _ = (&acp.FakeAgent{In: agentIn, Out: agentOut, PromptStarted: prompts}).Run() }()
	host := &acp.Client{In: clientIn, Out: clientOut, Perm: acp.DenyUnmatched{}, Wait: func(context.Context, string, acp.PermissionParams) acp.Decision {
		return acp.Decision{}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	in, _ := json.Marshal(map[string]string{"body": "look"})
	data := "iVBORw0KGgo="
	_, err := HostACP(ctx, &Assignment{
		Input: in,
		Attachments: []PromptAttachment{{
			Name: "shot.png",
			MIME: "image/png",
			Data: data,
		}},
	}, host, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-prompts:
		if len(p.Prompt) != 2 {
			t.Fatalf("blocks %+v", p.Prompt)
		}
		if p.Prompt[0].Type != "text" || !strings.Contains(p.Prompt[0].Text, "look") {
			t.Fatalf("text %+v", p.Prompt[0])
		}
		img := p.Prompt[1]
		if img.Type != "image" || img.MimeType != "image/png" || img.Data != data {
			t.Fatalf("image %+v", img)
		}
	default:
		t.Fatal("guest was not prompted")
	}
}

func TestHostACPReadsAttachmentFromPath(t *testing.T) {
	t.Parallel()
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	prompts := make(chan acp.PromptParams, 1)
	go func() { _ = (&acp.FakeAgent{In: agentIn, Out: agentOut, PromptStarted: prompts}).Run() }()
	host := &acp.Client{In: clientIn, Out: clientOut, Perm: acp.DenyUnmatched{}, Wait: func(context.Context, string, acp.PermissionParams) acp.Decision {
		return acp.Decision{}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data := "iVBORw0KGgo="
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	in, _ := json.Marshal(map[string]string{"body": "look"})
	_, err = HostACP(ctx, &Assignment{
		Input: in,
		Attachments: []PromptAttachment{{
			Name: "shot.png",
			MIME: "image/png",
			Path: path,
		}},
	}, host, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-prompts:
		if len(p.Prompt) != 2 {
			t.Fatalf("blocks %+v", p.Prompt)
		}
		img := p.Prompt[1]
		if img.Type != "image" || img.MimeType != "image/png" || img.Data != data {
			t.Fatalf("image %+v", img)
		}
	default:
		t.Fatal("guest was not prompted")
	}
}

func TestHostACPSendsPDFNestedResource(t *testing.T) {
	t.Parallel()
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	prompts := make(chan acp.PromptParams, 1)
	go func() { _ = (&acp.FakeAgent{In: agentIn, Out: agentOut, PromptStarted: prompts}).Run() }()
	host := &acp.Client{In: clientIn, Out: clientOut, Perm: acp.DenyUnmatched{}, Wait: func(context.Context, string, acp.PermissionParams) acp.Decision {
		return acp.Decision{}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	in, _ := json.Marshal(map[string]string{"body": "read"})
	data := base64.StdEncoding.EncodeToString([]byte("%PDF-1.1"))
	_, err := HostACP(ctx, &Assignment{
		Input: in,
		Attachments: []PromptAttachment{{
			Name: "doc.pdf",
			MIME: "application/pdf",
			Data: data,
		}},
	}, host, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-prompts:
		if len(p.Prompt) != 2 {
			t.Fatalf("blocks %+v", p.Prompt)
		}
		pdf := p.Prompt[1]
		if pdf.Type != "resource" || pdf.MimeType != "" || pdf.Data != "" {
			t.Fatalf("flat fields %+v", pdf)
		}
		if pdf.Resource == nil || pdf.Resource.MimeType != "application/pdf" || pdf.Resource.Blob != data {
			t.Fatalf("resource %+v", pdf.Resource)
		}
		raw, err := json.Marshal(pdf)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"resource"`) || strings.Contains(string(raw), `"data"`) {
			t.Fatalf("wire %s", raw)
		}
	default:
		t.Fatal("guest was not prompted")
	}
}

func TestHostACPNormalizesPlainTextMIME(t *testing.T) {
	t.Parallel()
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	prompts := make(chan acp.PromptParams, 1)
	go func() { _ = (&acp.FakeAgent{In: agentIn, Out: agentOut, PromptStarted: prompts}).Run() }()
	host := &acp.Client{In: clientIn, Out: clientOut, Perm: acp.DenyUnmatched{}, Wait: func(context.Context, string, acp.PermissionParams) acp.Decision {
		return acp.Decision{}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	in, _ := json.Marshal(map[string]string{"body": "read"})
	data := base64.StdEncoding.EncodeToString([]byte("hello notes"))
	_, err := HostACP(ctx, &Assignment{
		Input: in,
		Attachments: []PromptAttachment{{
			Name: "notes",
			MIME: "text/plain; charset=utf-8",
			Data: data,
		}},
	}, host, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-prompts:
		if len(p.Prompt) != 2 {
			t.Fatalf("blocks %+v", p.Prompt)
		}
		doc := p.Prompt[1]
		if doc.Type != "resource" || doc.Resource == nil || doc.Resource.MimeType != "text/plain" || doc.Resource.Blob != data {
			t.Fatalf("resource %+v", doc)
		}
	default:
		t.Fatal("guest was not prompted")
	}
}

func TestHTTPRecorderWaitCancelsBlockedApprovalPoll(t *testing.T) {
	pollStarted := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"id":"approval-1"}`)
			return
		}
		select {
		case pollStarted <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer func() {
		close(release)
		server.Close()
	}()
	recorder := &HTTPRecorder{Base: server.URL, Token: "turn-token", TurnID: 1, HTTP: server.Client()}
	id, err := recorder.Record(acp.Receipt{Type: acp.ActionApproval})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan acp.Decision, 1)
	go func() { done <- recorder.Wait(ctx, id, acp.PermissionParams{}) }()
	select {
	case <-pollStarted:
	case <-time.After(time.Second):
		t.Fatal("approval poll did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("approval wait did not return after cancellation")
	}
}

func TestHeartbeatSteersRenewsLeaseWhileDeliveryIsBlocked(t *testing.T) {
	var heartbeatCount atomic.Int32
	type heartbeatObservation struct {
		turnID int64
		count  int32
		acks   []int64
	}
	observations := make(chan heartbeatObservation, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			AckSteerIDs []int64 `json:"ack_steer_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		var turnID int64
		switch r.URL.Path {
		case "/jobs/1/heartbeat":
			turnID = 1
		case "/jobs/2/heartbeat":
			turnID = 2
		}
		count := heartbeatCount.Add(1)
		observations <- heartbeatObservation{turnID: turnID, count: count, acks: request.AckSteerIDs}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"steer": engine.Steer{ID: int64(count), Prompt: "prompt"},
		})
	}))
	defer server.Close()

	client := &Client{Base: server.URL, HTTP: server.Client()}
	assignment := &Assignment{TurnID: 1, LeaseGeneration: 1, ClaimedRevision: 1, TurnToken: "turn-token"}
	steers := make(chan engine.Steer)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		client.heartbeatSteers(context.Background(), assignment, steers, stop, func(error) {})
		close(done)
	}()
	for want := int32(1); want <= 3; want++ {
		select {
		case got := <-observations:
			if got.turnID != 1 || got.count != want {
				t.Fatalf("heartbeat observation %+v, want turn 1 count %d", got, want)
			}
			if want == 1 && len(got.acks) != 0 {
				t.Fatalf("first heartbeat acknowledged steers %v", got.acks)
			}
			if want > 1 && (len(got.acks) != 1 || got.acks[0] != int64(want-1)) {
				t.Fatalf("heartbeat %d acknowledged steers %v", want, got.acks)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d heartbeats arrived while steer delivery was blocked", want-1)
		}
	}
	close(stop)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat loop did not stop")
	}
	nextAssignment := &Assignment{TurnID: 2, LeaseGeneration: 1, ClaimedRevision: 1, TurnToken: "next-token"}
	if _, err := client.PollHeartbeat(nextAssignment); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-observations:
			if got.turnID == 2 {
				if len(got.acks) != 0 {
					t.Fatalf("next assignment inherited steer receipts %v", got.acks)
				}
				return
			}
		case <-deadline:
			t.Fatal("next assignment heartbeat was not observed")
		}
	}
}

func TestSteerClearsInterruptedRunResult(t *testing.T) {
	dir := t.TempDir()
	resultPath := filepath.Join(dir, "result.json")
	if err := os.WriteFile(resultPath, []byte(`{"blocked_reason":"stale result"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	promptStarted := make(chan acp.PromptParams, 2)
	agent := &acp.FakeAgent{
		In:            agentIn,
		Out:           agentOut,
		PromptStarted: promptStarted,
		PromptHandler: func(p acp.PromptParams, cancel <-chan struct{}, _ func(string, any) error) (acp.PromptResult, error) {
			if strings.Contains(p.Prompt[0].Text, "original prompt") {
				<-cancel
				return acp.PromptResult{StopReason: "cancelled"}, nil
			}
			return acp.PromptResult{StopReason: "end_turn"}, nil
		},
	}
	go func() { _ = agent.Run() }()
	host := &acp.Client{In: clientIn, Out: clientOut, Perm: acp.DenyUnmatched{}}
	in, _ := json.Marshal(map[string]string{"body": "original prompt"})
	a := &Assignment{Repo: "example/test-repo", Item: 1, ItemKind: "run", Input: in, ResultPath: resultPath}
	steers := make(chan engine.Steer)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		select {
		case <-promptStarted:
			steers <- engine.Steer{ID: 41, Prompt: "Use the corrected plan"}
		case <-ctx.Done():
		}
		close(done)
	}()
	art, steerIDs, err := hostACP(ctx, a, host, dir, steers, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("steer was not delivered")
	}
	select {
	case prompt := <-promptStarted:
		if !strings.Contains(prompt.Prompt[0].Text, "Use the corrected plan") || !strings.Contains(prompt.Prompt[0].Text, resultInstructions) {
			t.Fatalf("steered prompt did not retain run result instructions: %q", prompt.Prompt[0].Text)
		}
	case <-ctx.Done():
		t.Fatal("replacement prompt did not start")
	}
	if len(steerIDs) != 1 || steerIDs[0] != 41 {
		t.Fatalf("completed steer IDs %v", steerIDs)
	}
	if result := collectResult(nil, a, dir, art.SnapshotHash); result != nil {
		t.Fatalf("interrupted prompt's stale report was accepted: %+v", result)
	}
}

// Only a refused heartbeat ends the turn: 409 (lease not current) and 401
// (turn credential replaced by a new claim). A plane error or an
// unreachable plane keeps the harness running (#472).
func TestHeartbeatSteersEndsTurnOnlyWhenLeaseIsGone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{name: "lease rejected", status: http.StatusConflict},
		{name: "credential replaced", status: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch calls.Add(1) {
				case 1:
					// Transport failure: the connection drops without a response.
					hj, ok := w.(http.Hijacker)
					if !ok {
						t.Error("no hijacker")
						return
					}
					conn, _, err := hj.Hijack()
					if err == nil {
						_ = conn.Close()
					}
				case 2:
					http.Error(w, "database is locked", http.StatusInternalServerError)
				case 3:
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
				default:
					http.Error(w, "reject", tc.status)
				}
			}))
			defer server.Close()
			client := &Client{Base: server.URL, HTTP: server.Client()}
			a := &Assignment{TurnID: 1, LeaseGeneration: 1, ClaimedRevision: 1, TurnToken: "turn-token"}
			lost := make(chan error, 4)
			done := make(chan struct{})
			go func() {
				client.heartbeatSteers(context.Background(), a, make(chan engine.Steer), make(chan struct{}), func(err error) { lost <- err })
				close(done)
			}()
			select {
			case err := <-lost:
				if !errors.Is(err, ErrLeaseLost) {
					t.Fatalf("lost err %v", err)
				}
				if n := calls.Load(); n != 4 {
					t.Fatalf("turn ended after heartbeat %d, want the refused fourth", n)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("refused heartbeat did not end the turn after %d heartbeats", calls.Load())
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("heartbeat loop kept running after the lease was lost")
			}
		})
	}
}

func TestHostACPRegistryCodexResumesNativeThread(t *testing.T) {
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() { _ = clientIn.Close(); _ = clientOut.Close(); _ = agentIn.Close(); _ = agentOut.Close() })
	methods := make(chan string, 6)
	go func() {
		sc := bufio.NewScanner(agentIn)
		for sc.Scan() {
			var msg struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params struct {
					ThreadID string            `json:"threadId"`
					APIKey   string            `json:"apiKey"`
					Type     string            `json:"type"`
					Config   map[string]string `json:"config"`
				} `json:"params"`
			}
			if json.Unmarshal(sc.Bytes(), &msg) != nil {
				return
			}
			methods <- msg.Method
			var result any
			switch msg.Method {
			case "initialize":
				result = map[string]string{"userAgent": "codex-fixture"}
			case "initialized":
				continue
			case "account/login/start":
				if msg.Params.APIKey != "fixture-turn-grant" || msg.Params.Type != "apiKey" {
					return
				}
				result = map[string]string{"type": "apiKey"}
			case "thread/resume":
				if msg.Params.ThreadID != "existing-thread" {
					return
				}
				_, _ = io.WriteString(agentOut, `{"jsonrpc":"2.0","id":2,"error":{"code":-32000,"message":"thread not found"}}`+"\n")
				continue
			case "thread/start":
				if msg.Params.Config["openai_base_url"] != "http://127.0.0.1/model-proxy/v1" {
					return
				}
				result = map[string]any{"thread": map[string]string{"id": "replacement-thread"}}
			case "turn/start":
				_, _ = io.WriteString(agentOut, `{"jsonrpc":"2.0","method":"turn/completed"}`+"\n")
				return
			default:
				return
			}
			raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
			_, _ = agentOut.Write(append(raw, '\n'))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	host := &acp.Client{In: clientIn, Out: clientOut}
	art, err := HostACP(ctx, &Assignment{TurnToken: "fixture-turn-grant", ModelBaseURL: "http://127.0.0.1/model-proxy", Guest: "codex", GuestSpec: guest.Builtin()["codex"], GuestSessionID: "existing-thread", Input: json.RawMessage(`{"body":"continue"}`), Repo: "example/test-repo", Item: 1, ItemKind: "issue"}, host, t.TempDir())
	if err != nil || art.GuestSessionID != "replacement-thread" {
		t.Fatalf("native Codex resume: cursor=%q error=%v", art.GuestSessionID, err)
	}
	for _, want := range []string{"initialize", "initialized", "account/login/start", "thread/resume", "thread/start", "turn/start"} {
		if got := <-methods; got != want {
			t.Fatalf("native method %q, want %q", got, want)
		}
	}
}

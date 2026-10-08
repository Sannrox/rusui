package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/acp"
)

func TestClaudeArgvRequiresPrint(t *testing.T) {
	argv, err := Argv(KindClaude)
	if err != nil {
		t.Fatal(err)
	}
	if len(argv) < 2 || argv[0] != "claude" || argv[1] != "--print" {
		t.Fatalf("stream-json needs --print, got %q", argv)
	}
}

func TestArgvMatchesRunnerSpawn(t *testing.T) {
	for _, kind := range []string{KindGrok, KindClaude, KindCodex, KindShikigami} {
		want, err := Argv(kind)
		if err != nil {
			t.Fatal(err)
		}
		got, err := acp.SpawnArgsFor(kind)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("%s spawn %q provider %q", kind, got, want)
		}
	}
}

func TestConformanceSuite(t *testing.T) {
	t.Logf("grok acp:%d claude %s %s codex %s", GrokACPVersion, ClaudeStreamProto, ClaudeCodeVersion, CodexAppServerProto)
	for _, kind := range []string{KindGrok, KindClaude, KindCodex} {
		t.Run(kind, func(t *testing.T) {
			runConformance(t, kind)
		})
	}
}

func runConformance(t *testing.T, kind string) {
	t.Helper()
	workspace := t.TempDir()
	marker := filepath.Join(workspace, "keep.txt")
	if err := os.WriteFile(marker, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Revert(kind, workspace); !errors.Is(err, ErrNoRewind) {
		t.Fatalf("revert %v", err)
	}
	got, err := os.ReadFile(marker)
	if err != nil || string(got) != "unchanged" {
		t.Fatalf("revert changed the workspace %q %v", got, err)
	}
	outside := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(outside, []byte("pic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AttachmentOutside(workspace, outside); err != nil {
		t.Fatal(err)
	}
	if err := AttachmentOutside(workspace, marker); err == nil {
		t.Fatal("attachment inside the workspace was accepted")
	}

	first := drive(t, kind, Turn{Prompt: "start", Workspace: workspace, Attachments: []string{outside}}, true)
	var history []Event
	history = append(history, first.Events...)
	if !hasKind(history, "permission") {
		t.Fatalf("events %+v", history)
	}
	if !hasKind(history, "tool_call") || !hasKind(history, "transcript") {
		t.Fatalf("events %+v", history)
	}
	// Grok may omit a reject option. Codex and Claude support denial
	// unconditionally, so their gates decide.
	for _, ev := range history {
		if kind == KindGrok && ev.Kind == "permission" && ev.OptionID != "deny" {
			t.Fatalf("permission option %q", ev.OptionID)
		}
	}
	if kind == KindClaude {
		var limit Event
		for _, ev := range history {
			if ev.Kind == "usage_limit" {
				limit = ev
			}
		}
		if !limit.ResetMissing || limit.Reset != "" {
			t.Fatalf("usage limit %+v", limit)
		}
	}
	var questions Questions
	if kind == KindCodex {
		for _, ev := range history {
			if ev.Kind == "question" {
				questions.Add(Question{ID: ev.OptionID, Body: ev.Body})
			}
		}
		if len(questions.List()) != 1 {
			t.Fatal("missing async question")
		}
	}

	second := drive(t, kind, Turn{Prompt: "resume", Cursor: first.Cursor, Workspace: workspace}, false)
	history = append(history, second.Events...)
	if kind == KindClaude {
		if first.Cursor == "" || second.Cursor == "" {
			t.Fatal("claude cursor")
		}
	} else {
		if strings.Count(eventsText(history), "hello-"+kind) != 1 {
			t.Fatalf("resume duplicated history %s", eventsText(history))
		}
		if !strings.Contains(eventsText(second.Events), "resumed") {
			t.Fatalf("resume events %+v", second.Events)
		}
	}
	if kind == KindCodex {
		if len(questions.List()) != 1 || questions.List()[0].Body != "which file" {
			t.Fatal("question did not survive restart")
		}
		questions.Dismiss("q1")
		if !questions.List()[0].Dismissed {
			t.Fatal("dismiss")
		}
	}
}

func TestInstancesDoNotShareState(t *testing.T) {
	root := t.TempDir()
	a, err := NewInstance(KindClaude, "one", root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewInstance(KindClaude, "two", root)
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(a.Dir, "auth.json")
	if err := os.WriteFile(secret, []byte("account-one"), 0o600); err != nil {
		t.Fatal(err)
	}
	env, err := LaunchEnv(b, []string{"PATH=/usr/bin", "ANTHROPIC_API_KEY=sk", "CLAUDE_CONFIG_DIR=" + a.Dir})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, a.Dir) || strings.Contains(joined, "account-one") || strings.Contains(joined, "sk") {
		t.Fatalf("second instance saw the first: %s", joined)
	}
	if !strings.Contains(joined, "CLAUDE_CONFIG_DIR="+b.Dir) {
		t.Fatal(joined)
	}
	if _, err := LaunchEnv(b, []string{"HOME=" + a.Dir}); err == nil {
		t.Fatal("shared HOME accepted")
	}
	if err := os.WriteFile(filepath.Join(b.Dir, copiedHomeMarker), []byte(a.Dir), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LaunchEnv(b, nil); err == nil {
		t.Fatal("copied home accepted")
	}
}

func TestProbeDoesNotStartSession(t *testing.T) {
	for _, kind := range []string{KindGrok, KindClaude, KindCodex} {
		cmd := fakeCommand(t, kind, true)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s probe %v %s", kind, err, stderr.String())
		}
		ver, err := PinnedVersion(kind)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout.String(), ver) || strings.Contains(stdout.String(), "session") || strings.Contains(stderr.String(), "session") {
			t.Fatalf("%s probe out %q err %q", kind, stdout.String(), stderr.String())
		}
	}
}

func TestProcessKillEndsTheTurn(t *testing.T) {
	cmd := fakeCommand(t, KindGrok, false)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := Run(context.Background(), KindGrok, Instance{Kind: KindGrok, ID: "k", Dir: t.TempDir()}, stdio{Reader: stdout, WriteCloser: stdin}, Turn{Prompt: "start", Workspace: t.TempDir()}, func([]Option, json.RawMessage) (string, bool) {
			return "", false
		})
		done <- err
	}()
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("killed provider still completed the turn")
	}
}

func TestUnknownProtocolRefused(t *testing.T) {
	r, w := io.Pipe()
	go func() {
		defer func() { _ = w.Close() }()
		_, _ = w.Write([]byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocolVersion\":99}}\n"))
	}()
	_, err := runGrok(stdio{Reader: r, WriteCloser: nopWriteCloser{io.Discard}}, Turn{}, nil)
	// runGrok writes first, so Discard is fine and the pipe supplies the bad version.
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatal(err)
	}
}

func drive(t *testing.T, kind string, turn Turn, wantDeny bool) Result {
	t.Helper()
	cmd := fakeCommand(t, kind, false)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	// History is what an observer saw live; the Claude result keeps no
	// transcript bodies (#344).
	var observed []Event
	turn.Observe = func(ev Event) { observed = append(observed, ev) }
	res, err := Run(context.Background(), kind, Instance{Kind: kind, ID: "t", Dir: t.TempDir()}, stdio{Reader: stdout, WriteCloser: stdin}, turn, func(options []Option, _ json.RawMessage) (string, bool) {
		if len(options) == 0 {
			return "", false
		}
		if kind == KindCodex && wantDeny {
			return "decline", false
		}
		return options[0].ID, true // Grok omitted reject; its adapter must still deny
	})
	if err != nil {
		t.Fatalf("%s run %v", kind, err)
	}
	res.Events = observed
	if wantDeny && kind != KindClaude {
		ok := false
		for _, ev := range res.Events {
			if ev.Kind == "permission" && (ev.OptionID == "deny" || ev.OptionID == "decline") {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("%s did not deny %+v", kind, res.Events)
		}
	}
	return res
}

func fakeCommand(t *testing.T, kind string, probe bool) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	mode := "session"
	if probe {
		mode = "probe"
		argv, err := ProbeArgv(kind)
		if err != nil {
			t.Fatal(err)
		}
		if argv[len(argv)-1] != "--version" {
			t.Fatalf("probe argv %v", argv)
		}
	}
	cmd.Env = append(os.Environ(), "RUSUI_PROVIDER_FAKE="+kind, "RUSUI_PROVIDER_FAKE_MODE="+mode)
	return cmd
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

type stdio struct {
	io.Reader
	io.WriteCloser
}

func (s stdio) Close() error { return s.WriteCloser.Close() }

func hasKind(events []Event, kind string) bool {
	for _, ev := range events {
		if ev.Kind == kind {
			return true
		}
	}
	return false
}

func eventsText(events []Event) string {
	var b strings.Builder
	for _, ev := range events {
		b.WriteString(ev.Body)
		b.WriteByte('\n')
	}
	return b.String()
}

// The prompt goes first, but a wrong init still refuses the turn before any
// later event (here a permission request) is acted on.
func TestClaudeInitMismatchRefusedBeforeAnyDecision(t *testing.T) {
	for name, tc := range map[string]struct {
		version, session, cursor, want string
	}{
		"version": {version: "0.0.1", session: "s", want: "protocol refused"},
		"cursor":  {version: ClaudeCodeVersion, session: "other", cursor: "prior", want: "cursor mismatch"},
	} {
		t.Run(name, func(t *testing.T) {
			r, w := io.Pipe()
			go func() {
				defer func() { _ = w.Close() }()
				_, _ = fmt.Fprintf(w, `{"type":"system","subtype":"init","claude_code_version":%q,"session_id":%q}`+"\n", tc.version, tc.session)
				_, _ = fmt.Fprintln(w, `{"type":"control_request","request_id":"p","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"ls"}}}`)
			}()
			decided := false
			_, err := runClaude(stdio{Reader: r, WriteCloser: nopWriteCloser{io.Discard}}, Turn{Prompt: "x", Cursor: tc.cursor},
				func([]Option, json.RawMessage) (string, bool) { decided = true; return "", false })
			if err == nil || !strings.Contains(err.Error(), tc.want) || decided {
				t.Fatalf("err=%v decided=%v", err, decided)
			}
		})
	}
}

// Claude's can_use_tool reaches the gate in ACP shape and the reply is a
// control_response; denial and unknown subtypes answer deny.
func TestClaudeControlRequestUsesGateAndReplies(t *testing.T) {
	for name, tc := range map[string]struct {
		line      string
		allow     bool
		wantKind  string
		wantCmd   string
		behaviour string
	}{
		"bash allowed":    {`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"gh pr merge 1"}}}`, true, "execute", "gh pr merge 1", "allow"},
		"write denied":    {`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Write","input":{"file_path":"/tmp/x"}}}`, false, "edit", "", "deny"},
		"subagent":        {`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Agent","input":{}}}`, true, "think", "", "allow"},
		"todos":           {`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"TodoWrite","input":{}}}`, true, "think", "", "allow"},
		"plan mode":       {`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"ExitPlanMode","input":{}}}`, true, "switch_mode", "", "allow"},
		"skill":           {`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Skill","input":{}}}`, true, "read", "", "allow"},
		"mcp tool":        {`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"mcp__github__create_issue","input":{}}}`, true, "other", "", "allow"},
		"unknown tool":    {`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Frobnicate","input":{}}}`, true, "other", "", "allow"},
		"unknown subtype": {`{"type":"control_request","request_id":"r1","request":{"subtype":"interrupt"}}`, true, "", "", "deny"},
		"ask user":        {`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","input":{"questions":[]}}}`, true, "", "", "deny"},
	} {
		t.Run(name, func(t *testing.T) {
			var seen map[string]any
			var out strings.Builder
			ev, err := answerClaudeControl(&out, []byte(tc.line), func(_ []Option, raw json.RawMessage) (string, bool) {
				_ = json.Unmarshal(raw, &seen)
				return "allow", tc.allow
			})
			if tc.wantKind == "" && seen != nil {
				t.Fatalf("gate consulted for %s: %v", name, seen)
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantKind != "" && (seen["kind"] != tc.wantKind || (tc.wantCmd != "" && seen["command"] != tc.wantCmd)) {
				t.Fatalf("gate saw %v", seen)
			}
			var reply struct {
				Type     string `json:"type"`
				Response struct {
					RequestID string `json:"request_id"`
					Response  struct {
						Behavior string `json:"behavior"`
					} `json:"response"`
				} `json:"response"`
			}
			if err := json.Unmarshal([]byte(out.String()), &reply); err != nil || reply.Type != "control_response" ||
				reply.Response.RequestID != "r1" || reply.Response.Response.Behavior != tc.behaviour || ev.OptionID != tc.behaviour {
				t.Fatalf("reply %s event %+v", out.String(), ev)
			}
		})
	}
}

// Claude's assistant text, tool calls, and tool results become transcript
// and tool_call events as they are read, in stream order. Thinking is not
// recorded, a tool input keeps only locator fields, and a tool result
// keeps no content.
func TestClaudeStreamRecordsTextAndToolCalls(t *testing.T) {
	stream := claudeTextLine("I'll look at the files.") +
		`{"type":"assistant","message":{"id":"msg_01Think","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"private plan","signature":"sig"}]},"parent_tool_use_id":null,"session_id":"claude-sess","uuid":"u0"}` + "\n" +
		claudeToolUseLine + claudeToolResultLine +
		`{"type":"assistant","message":{"id":"msg_01Write","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_02DEF","name":"Write","input":{"file_path":"/workspace/a.txt","content":"file body"}},{"type":"tool_use","id":"toolu_03GHI","name":"mcp__github__create_issue","input":{"title":"new issue","body":"issue body"}}]},"parent_tool_use_id":null,"session_id":"claude-sess","uuid":"u4"}` + "\n" +
		`{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_02DEF","type":"tool_result","content":[{"type":"text","text":"denied"}],"is_error":true}]},"parent_tool_use_id":null,"session_id":"claude-sess","uuid":"u5"}` + "\n" +
		`{"type":"result","subtype":"success","is_error":false,"result":"Done.","session_id":"claude-sess"}` + "\n"
	r, w := io.Pipe()
	go func() {
		defer func() { _ = w.Close() }()
		_, _ = fmt.Fprintf(w, `{"type":"system","subtype":"init","claude_code_version":%q,"session_id":"claude-sess"}`+"\n", ClaudeCodeVersion)
		_, _ = io.WriteString(w, stream)
	}()
	var observed []Event
	res, err := runClaude(stdio{Reader: r, WriteCloser: nopWriteCloser{io.Discard}}, Turn{Prompt: "x", Observe: func(ev Event) { observed = append(observed, ev) }}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"transcript I'll look at the files.",
		"tool_call toolu_01ABC Bash execute pending command=ls -la description=List files",
		"tool_call toolu_01ABC completed",
		"tool_call toolu_02DEF Write edit pending file_path=/workspace/a.txt",
		"tool_call toolu_03GHI mcp__github__create_issue other pending",
		"tool_call toolu_02DEF failed",
	}
	got := make([]string, 0, len(observed))
	for _, ev := range observed {
		line := ev.Kind
		if ev.Tool == nil {
			line += " " + ev.Body
		} else {
			line += " " + ev.Tool.ID
			if ev.Tool.Name != "" {
				line += " " + ev.Tool.Name + " " + ev.Tool.Kind
			}
			line += " " + ev.Tool.Status
			for _, k := range []string{"command", "description", "file_path"} {
				if v, ok := ev.Tool.Input[k]; ok {
					line += " " + k + "=" + v
				}
			}
		}
		got = append(got, line)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(res.Events) != 0 {
		t.Fatalf("result keeps transcript bodies %+v", res.Events)
	}
	write := observed[3].Tool.Input
	if _, ok := write["content"]; ok {
		t.Fatalf("write body recorded %v", write)
	}
	if mcp := observed[4].Tool.Input; len(mcp) != 1 || mcp["title"] != "new issue" {
		t.Fatalf("mcp input %v", mcp)
	}
	if text := eventsText(observed); strings.Contains(text, "private plan") || strings.Contains(text, "keep.txt") || strings.Contains(text, "denied") {
		t.Fatalf("thinking or tool result content recorded %s", text)
	}
}

func TestCodexFreshHomeCanStartAfterMissingCursor(t *testing.T) {
	result := drive(t, KindCodex, Turn{Cursor: "missing-thread", Prompt: "continue", Workspace: t.TempDir(), ModelBaseURL: "http://127.0.0.1:1234", Model: "fixture-model"}, true)
	if result.Cursor != "codex-thread" {
		t.Fatalf("fresh thread cursor: %s", result.Cursor)
	}
}

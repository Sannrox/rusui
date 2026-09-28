package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	for _, kind := range []string{KindGrok, KindClaude, KindCodex} {
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
	if kind != KindClaude && (!hasKind(history, "tool_call") || !hasKind(history, "transcript")) {
		t.Fatalf("events %+v", history)
	}
	for _, ev := range history {
		if ev.Kind == "permission" && ev.OptionID != "deny" {
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
	res, err := Run(context.Background(), kind, Instance{Kind: kind, ID: "t", Dir: t.TempDir()}, stdio{Reader: stdout, WriteCloser: stdin}, turn, func(options []Option, _ json.RawMessage) (string, bool) {
		if len(options) == 0 {
			return "", false
		}
		return options[0].ID, true // the provider omitted reject; adapter must still deny
	})
	if err != nil {
		t.Fatalf("%s run %v", kind, err)
	}
	if wantDeny {
		ok := false
		for _, ev := range res.Events {
			if ev.Kind == "permission" && ev.OptionID == "deny" {
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

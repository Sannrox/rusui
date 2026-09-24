package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/server"
	"github.com/sannrox/rusui/internal/store"
)

const attachPolicy = `version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
  review: true
  comments: false
  close: false
  implement: false
  land: false
  max_reviews_per_repo_per_utc_day: 50
projects:
  test:
    repos:
      example/test-repo:
        visibility: public
        review: true
        comments: true
        close: true
        implement: false
        land: false
`

func TestRunAttachManagedUsesOperatorLeaseAndAudit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "managed.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	pol, err := policy.Parse([]byte(attachPolicy))
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(st, pol, gh.NewFake(), clock.Real{})
	e.ReloadPolicy(pol)
	rt := &env.FakeRuntime{}
	input := &lockedBuffer{}
	rt.StdioHook = func(string, []string, []string) (io.WriteCloser, io.ReadCloser, func(), error) {
		return input, io.NopCloser(strings.NewReader("managed-ready\n")), func() {}, nil
	}
	e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	sid, err := e.StartRun("test", "term", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(st, sid)
	if err != nil {
		t.Fatal(err)
	}
	envRow, err := store.GetEnvironment(st, sess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	envRow.Driver, envRow.Handle, envRow.State = env.KindContainer, "ctr-term", store.EnvReady
	if err := store.UpdateEnvironment(st, *envRow); err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer((&server.Server{Eng: e, WorkerSec: "worker-token", OperatorTok: "op-token"}).Handler())
	t.Cleanup(hs.Close)

	stdin, sendInput := io.Pipe()
	stdout := &signalBuffer{signal: "managed-ready\n", ready: make(chan struct{})}
	var stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runAttach(attachOptions{URL: hs.URL, Token: "op-token", DB: dbPath}, sid, stdin, stdout, &stderr)
	}()
	waitForAttachSignal(t, stdout.ready, done, &stderr)
	_, _ = io.WriteString(sendInput, "echo hello\n")
	_ = sendInput.Close()
	if err := <-done; err != nil {
		t.Fatalf("runAttach: %v (stderr: %s)", err, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "managed-ready\n") {
		t.Fatalf("terminal output %q", got)
	}
	if got := input.String(); got != "echo hello\n" {
		t.Fatalf("managed input %q", got)
	}
	for _, action := range []string{"acquire", "input", "revoke"} {
		count, err := store.CountTerminalAccess(st, envRow.ID, action)
		if err != nil || count != 1 {
			t.Fatalf("terminal %s audit count=%d err=%v", action, count, err)
		}
	}
	lease, found, err := store.GetTerminalLease(st, envRow.ID)
	if err != nil || !found || time.Now().UTC().Before(lease.ExpiresAt) {
		t.Fatalf("terminal lease not released: lease=%+v found=%v err=%v", lease, found, err)
	}

	if err := runAttach(attachOptions{URL: hs.URL, Token: "worker-token", DB: dbPath}, sid, strings.NewReader(""), io.Discard, io.Discard); err == nil {
		t.Fatal("worker token opened the operator terminal")
	}
}

func TestRunAttachLocalUsesDirectSumikaAndCtrlBracketDetach(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "local.db")
	cwd := filepath.Join(dir, "workspace")
	if err := os.Mkdir(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	policyText := `version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
projects:
  local-test:
    repos: {}
    session_kinds: [local]
    local_runtime:
      argv: ["/bin/sh", "-i"]
      cwd: "` + cwd + `"
`
	pol, err := policy.Parse([]byte(policyText))
	if err != nil {
		t.Fatal(err)
	}
	socketDir, err := os.MkdirTemp("/tmp", "r-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "s.sock")
	localInput := make(chan string, 1)
	startFakeSumika(t, socketPath, localInput)
	t.Setenv("SUMIKA_SOCK", socketPath)
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e := engine.New(st, pol, gh.NewFake(), clock.Real{})
	e.ReloadPolicy(pol)
	created, err := e.StartLocalSession("local-test", "")
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer((&server.Server{Eng: e, WorkerSec: "worker-token", OperatorTok: "op-token"}).Handler())
	t.Cleanup(hs.Close)

	stdin, sendInput := io.Pipe()
	stdout := &signalBuffer{signal: "local-ready\n", ready: make(chan struct{})}
	var stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runAttach(attachOptions{URL: hs.URL, Token: "op-token", DB: dbPath}, created.Session.ID, stdin, stdout, &stderr)
	}()
	waitForAttachSignal(t, stdout.ready, done, &stderr)
	_, _ = io.WriteString(sendInput, "hello\n\x1dignored")
	_ = sendInput.Close()
	if err := <-done; err != nil {
		t.Fatalf("runAttach: %v (stderr: %s)", err, stderr.String())
	}
	select {
	case got := <-localInput:
		if got != "hello\n" {
			t.Fatalf("local PTY input %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Sumika did not receive attached PTY input")
	}
	if !strings.Contains(stdout.String(), "local-ready\n") {
		t.Fatalf("local output %q", stdout.String())
	}
	process, err := store.LatestSumikaProcess(st, created.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	attaches, err := store.ListSumikaAttaches(st, process.ID)
	if err != nil || len(attaches) != 1 || attaches[0].State != store.AttachDetached {
		t.Fatalf("local attach lifecycle: %+v err=%v", attaches, err)
	}
	if err := runAttach(attachOptions{URL: hs.URL, Token: "worker-token", DB: dbPath}, created.Session.ID, strings.NewReader(""), io.Discard, io.Discard); err == nil {
		t.Fatal("worker token opened a local attach")
	}
}

type signalBuffer struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	signal string
	ready  chan struct{}
	once   sync.Once
}

func (w *signalBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if strings.Contains(w.buf.String(), w.signal) {
		w.once.Do(func() { close(w.ready) })
	}
	return n, err
}

func (w *signalBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func waitForAttachSignal(t *testing.T, ready <-chan struct{}, done <-chan error, stderr *bytes.Buffer) {
	t.Helper()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("attach ended before terminal output: %v (stderr: %s)", err, stderr.String())
	case <-time.After(3 * time.Second):
		t.Fatal("terminal output did not arrive")
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *lockedBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (*lockedBuffer) Close() error { return nil }

func (w *lockedBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

type sumikaRequest struct {
	Op      string   `json:"op"`
	Name    string   `json:"name"`
	Argv    []string `json:"argv"`
	Cwd     string   `json:"cwd"`
	Project *string  `json:"project"`
}

type sumikaSession struct {
	Name    string   `json:"name"`
	Argv    []string `json:"argv"`
	Cwd     string   `json:"cwd"`
	Project *string  `json:"project"`
	Status  string   `json:"status"`
	Focused bool     `json:"focused"`
}

// startFakeSumika serves Sumika's socket protocol until the returned stop
// is called or the test ends.
func startFakeSumika(t *testing.T, path string, input chan<- string) (stop func()) {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	var mu sync.Mutex
	var session sumikaSession
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer func() { _ = conn.Close() }()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					t.Errorf("read Sumika request: %v", err)
					return
				}
				var req sumikaRequest
				if err := json.Unmarshal(line, &req); err != nil {
					t.Errorf("decode Sumika request: %v", err)
					return
				}
				mu.Lock()
				if req.Op == "start" {
					session = sumikaSession{Name: req.Name, Argv: req.Argv, Cwd: req.Cwd, Project: req.Project, Status: "running", Focused: true}
				}
				current := session
				mu.Unlock()
				switch req.Op {
				case "start", "attach":
					if err := json.NewEncoder(conn).Encode(map[string]any{"ok": true, "session": current}); err != nil {
						t.Errorf("write Sumika response: %v", err)
						return
					}
					if req.Op == "attach" {
						_, _ = io.WriteString(conn, "local-ready\n")
						b, _ := io.ReadAll(conn)
						input <- string(b)
					}
				case "list":
					if err := json.NewEncoder(conn).Encode(map[string]any{"ok": true, "sessions": []sumikaSession{current}}); err != nil {
						t.Errorf("write Sumika list: %v", err)
					}
				case "resize":
					_ = json.NewEncoder(conn).Encode(map[string]any{"ok": true})
				default:
					t.Errorf("unexpected Sumika op %q", req.Op)
				}
			}(conn)
		}
	}()
	return func() { _ = listener.Close() }
}

// localAttachFixture starts a local Session on a fake Sumika daemon and a
// Rusui server over the same database.
func localAttachFixture(t *testing.T) (st *store.Store, url, dbPath string, sessionID int64, input chan string, stopSumika func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath = filepath.Join(dir, "local.db")
	cwd := filepath.Join(dir, "workspace")
	if err := os.Mkdir(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	pol, err := policy.Parse([]byte(`version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
projects:
  local-test:
    repos: {}
    session_kinds: [local]
    local_runtime:
      argv: ["/bin/sh", "-i"]
      cwd: "` + cwd + `"
`))
	if err != nil {
		t.Fatal(err)
	}
	socketDir, err := os.MkdirTemp("/tmp", "r-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "s.sock")
	input = make(chan string, 1)
	stopSumika = startFakeSumika(t, socketPath, input)
	t.Setenv("SUMIKA_SOCK", socketPath)
	if st, err = store.Open(dbPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e := engine.New(st, pol, gh.NewFake(), clock.Real{})
	e.ReloadPolicy(pol)
	created, err := e.StartLocalSession("local-test", "")
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer((&server.Server{Eng: e, WorkerSec: "worker-token", OperatorTok: "op-token"}).Handler())
	t.Cleanup(hs.Close)
	return st, hs.URL, dbPath, created.Session.ID, input, stopSumika
}

// assertNoLocalAttach checks that a refused attach recorded no Attach and
// sent nothing to the local PTY.
func assertNoLocalAttach(t *testing.T, st *store.Store, sessionID int64, input <-chan string) {
	t.Helper()
	process, err := store.LatestSumikaProcess(st, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if attaches, err := store.ListSumikaAttaches(st, process.ID); err != nil || len(attaches) != 0 {
		t.Fatalf("refused attach was recorded: %+v err=%v", attaches, err)
	}
	select {
	case got := <-input:
		t.Fatalf("refused attach reached the PTY: %q", got)
	default:
	}
}

func TestRunAttachLocalRefusesEndedProcess(t *testing.T) {
	for _, state := range []string{store.ProcessDead, store.ProcessLost} {
		t.Run(state, func(t *testing.T) {
			st, url, dbPath, sid, input, _ := localAttachFixture(t)
			process, err := store.LatestSumikaProcess(st, sid)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.ObserveSumikaProcess(st, process.ID, process.Generation, process.Revision, state, time.Now()); err != nil {
				t.Fatal(err)
			}
			err = runAttach(attachOptions{URL: url, Token: "op-token", DB: dbPath}, sid, strings.NewReader("x"), io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "not attachable") {
				t.Fatalf("attach to %s process: %v", state, err)
			}
			assertNoLocalAttach(t, st, sid, input)
		})
	}
}

func TestRunAttachLocalReportsMissingDaemon(t *testing.T) {
	st, url, dbPath, sid, input, stopSumika := localAttachFixture(t)
	stopSumika()
	if err := runAttach(attachOptions{URL: url, Token: "op-token", DB: dbPath}, sid, strings.NewReader("x"), io.Discard, io.Discard); err == nil {
		t.Fatal("attach succeeded without the Sumika daemon")
	}
	assertNoLocalAttach(t, st, sid, input)
}

func TestRunAttachRejectsBadOperatorToken(t *testing.T) {
	st, url, dbPath, sid, input, _ := localAttachFixture(t)
	for _, token := range []string{"", "wrong-token"} {
		if err := runAttach(attachOptions{URL: url, Token: token, DB: dbPath}, sid, strings.NewReader("x"), io.Discard, io.Discard); err == nil {
			t.Fatalf("token %q attached", token)
		}
	}
	assertNoLocalAttach(t, st, sid, input)
}

func TestRunAttachManagedRefusesExpiredEnvironment(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "managed.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	pol, err := policy.Parse([]byte(attachPolicy))
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(st, pol, gh.NewFake(), clock.Real{})
	e.ReloadPolicy(pol)
	e.Container = env.Container{RT: &env.FakeRuntime{}, Image: "rusui-guest:test"}
	sid, err := e.StartRun("test", "term", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(st, sid)
	if err != nil {
		t.Fatal(err)
	}
	envRow, err := store.GetEnvironment(st, sess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	envRow.Driver, envRow.Handle, envRow.State = env.KindContainer, "ctr-term", store.EnvExpired
	if err := store.UpdateEnvironment(st, *envRow); err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer((&server.Server{Eng: e, WorkerSec: "worker-token", OperatorTok: "op-token"}).Handler())
	t.Cleanup(hs.Close)
	err = runAttach(attachOptions{URL: hs.URL, Token: "op-token", DB: dbPath}, sid, strings.NewReader("echo hi\n"), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), store.EnvExpired) {
		t.Fatalf("attach to expired environment: %v", err)
	}
	if n, err := store.CountTerminalAccess(st, envRow.ID, "acquire"); err != nil || n != 0 {
		t.Fatalf("expired environment granted a lease: %d %v", n, err)
	}
}

func TestStreamManagedOutputKeepsTerminalWhitespace(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: \n\ndata:   $ ls\\t \n\ndata:    \n\n")
	}))
	t.Cleanup(hs.Close)
	var out bytes.Buffer
	if err := streamManagedOutput(context.Background(), hs.Client(), hs.URL, "op-token", 1, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "  $ ls\t    " {
		t.Fatalf("output %q", got)
	}
}

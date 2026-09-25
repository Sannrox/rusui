package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/term"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
	"github.com/sannrox/rusui/internal/sumika"
)

const terminalGenerationHeader = "X-Rusui-Terminal-Generation"

var (
	csrfPattern  = regexp.MustCompile(`name="csrf" value="([^"]+)"`)
	writePattern = regexp.MustCompile(`data-write="(true|false)"`)
	genPattern   = regexp.MustCompile(`data-gen="([0-9]+)"`)
)

type attachOptions struct {
	URL   string
	Token string
	DB    string
}

type attachDetail struct {
	Session          store.Session   `json:"session"`
	Turns            []store.Turn    `json:"turns"`
	Processes        []store.Process `json:"processes"`
	EnvironmentState string          `json:"environment_state"`
}

type terminalPage struct {
	csrf       string
	leaseHeld  bool
	generation int
}

func attachCLI(args []string) {
	fs := flag.NewFlagSet("attach", flag.ExitOnError)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_OPERATOR_TOKEN"), "operator token")
	db := fs.String("db", "rusui.db", "Rusui SQLite database (must match the server for local sessions)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: rusui attach [-url URL] [-token TOKEN] [-db PATH] SESSION_ID")
		os.Exit(2)
	}
	sessionID, err := strconv.ParseInt(fs.Arg(0), 10, 64)
	if err != nil || sessionID < 1 {
		_, _ = fmt.Fprintln(os.Stderr, "attach: invalid session id")
		os.Exit(2)
	}
	err = runAttach(attachOptions{URL: *url, Token: *token, DB: *db}, sessionID, os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "attach: %v\n", err)
		os.Exit(1)
	}
}

func runAttach(opts attachOptions, sessionID int64, stdin io.Reader, stdout, stderr io.Writer) error {
	if opts.Token == "" {
		return errors.New("set -token or RUSUI_OPERATOR_TOKEN")
	}
	client, err := planeHTTP(opts.URL)
	if err != nil {
		return err
	}
	detail, err := fetchAttachDetail(client, opts.URL, opts.Token, sessionID)
	if err != nil {
		return err
	}
	switch detail.Session.Kind {
	case store.SessionKindLocal:
		if _, err := fetchOperatorPage(client, opts.URL, opts.Token, "/console/sessions"); err != nil {
			return err
		}
		return attachLocal(opts.DB, sessionID, detail, stdin, stdout, stderr)
	case store.SessionKindReview, store.SessionKindRun, store.SessionKindScheduled:
		path := fmt.Sprintf("/console/sessions/%d/terminal", sessionID)
		pageBody, err := fetchOperatorPage(client, opts.URL, opts.Token, path)
		if err != nil {
			return err
		}
		if err := reportAttachIdentity(stderr, detail); err != nil {
			return fmt.Errorf("write session identity: %w", err)
		}
		if detail.EnvironmentState != store.EnvReady {
			return fmt.Errorf("environment %d is %q; managed terminal requires %q", detail.Session.EnvironmentID, detail.EnvironmentState, store.EnvReady)
		}
		page, err := parseTerminalPage(pageBody)
		if err != nil {
			return err
		}
		return attachManagedSession(client, opts.URL, opts.Token, sessionID, page, stdin, stdout, stderr)
	default:
		return fmt.Errorf("unsupported session runtime %q", detail.Session.Kind)
	}
}

func attachManagedSession(client *http.Client, baseURL, token string, sessionID int64, page terminalPage, stdin io.Reader, stdout, stderr io.Writer) error {
	stderr = &synchronizedWriter{writer: stderr}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	streamDone, err := startSessionEventStream(ctx, client, baseURL, token, sessionID, stderr)
	if err != nil {
		return fmt.Errorf("session transcript: %w", err)
	}
	terminalDone := make(chan error, 1)
	go func() {
		terminalDone <- attachManaged(ctx, client, baseURL, token, sessionID, page, stdin, stdout, stderr)
	}()
	select {
	case terminalErr := <-terminalDone:
		cancel()
		streamErr := <-streamDone
		if errors.Is(streamErr, context.Canceled) {
			streamErr = nil
		}
		if streamErr != nil {
			_, _ = fmt.Fprintf(stderr, "Session transcript stream disconnected: %v\n", streamErr)
			streamErr = fmt.Errorf("session transcript: %w", streamErr)
		}
		return errors.Join(terminalErr, streamErr)
	case streamErr := <-streamDone:
		if streamErr == nil {
			streamErr = io.ErrUnexpectedEOF
		}
		_, writeErr := fmt.Fprintf(stderr, "Session transcript stream disconnected: %v\n", streamErr)
		cancel()
		terminalErr := <-terminalDone
		if writeErr != nil {
			streamErr = errors.Join(streamErr, writeErr)
		}
		return errors.Join(terminalErr, fmt.Errorf("session transcript: %w", streamErr))
	}
}

type synchronizedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *synchronizedWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(data)
}

func startSessionEventStream(ctx context.Context, client *http.Client, baseURL, token string, sessionID int64, output io.Writer) (<-chan error, error) {
	path := fmt.Sprintf("/sessions/%d/attach", sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		return nil, fmt.Errorf("%s %s", res.Status, body)
	}
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		_ = res.Body.Close()
		return nil, errors.New("server did not return a session event stream")
	}
	done := make(chan error, 1)
	go func() {
		err := streamSessionEvents(res.Body, output)
		if err == nil && ctx.Err() == nil {
			err = io.ErrUnexpectedEOF
		}
		done <- err
	}()
	return done, nil
}

func streamSessionEvents(body io.ReadCloser, output io.Writer) error {
	defer func() { _ = body.Close() }()
	reader := bufio.NewReader(body)
	event := ""
	var data []string
	flush := func() error {
		if event == "" || len(data) == 0 {
			event, data = "", nil
			return nil
		}
		name := event
		payload := strings.Join(data, "\n")
		event, data = "", nil
		return writeSessionEvent(output, name, []byte(payload))
	}
	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
		} else if value, ok := strings.CutPrefix(line, "event:"); ok {
			event = strings.TrimSpace(value)
		} else if value, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(value, " "))
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return err
			}
			return flush()
		}
	}
}

func writeSessionEvent(output io.Writer, event string, data []byte) error {
	switch event {
	case "session":
		var session store.Session
		if err := json.Unmarshal(data, &session); err != nil {
			return fmt.Errorf("decode session event: %w", err)
		}
		_, err := fmt.Fprintf(output, "Session %d: %s (environment %d)\n", session.ID, session.State, session.EnvironmentID)
		return err
	case "turn":
		var turn store.Turn
		if err := json.Unmarshal(data, &turn); err != nil {
			return fmt.Errorf("decode turn event: %w", err)
		}
		_, err := fmt.Fprintf(output, "Turn %d: %s (revision %d)\n", turn.ID, turn.State, turn.ClaimedRevision)
		return err
	case "action":
		var action store.Action
		if err := json.Unmarshal(data, &action); err != nil {
			return fmt.Errorf("decode transcript event: %w", err)
		}
		_, err := fmt.Fprintf(output, "Transcript %s: %s\n", action.Type, action.Body)
		return err
	default:
		return nil
	}
}

func fetchAttachDetail(client *http.Client, baseURL, token string, sessionID int64) (attachDetail, error) {
	var detail attachDetail
	path := fmt.Sprintf("/sessions/%d", sessionID)
	res, err := doAuthorized(client, http.MethodGet, baseURL, path, token, nil)
	if err != nil {
		return detail, err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return detail, err
	}
	if res.StatusCode >= http.StatusMultipleChoices {
		return detail, fmt.Errorf("session detail: %s %s", res.Status, body)
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		return detail, fmt.Errorf("session detail: %w", err)
	}
	if detail.Session.ID != sessionID {
		return detail, errors.New("session detail identity mismatch")
	}
	return detail, nil
}

func fetchOperatorPage(client *http.Client, baseURL, token, path string) ([]byte, error) {
	res, err := doAuthorized(client, http.MethodGet, baseURL, path, token, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	want, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK || res.Request == nil || res.Request.URL.Path != path || !strings.EqualFold(res.Request.URL.Host, want.Host) {
		return nil, fmt.Errorf("operator terminal authorization failed: %s", res.Status)
	}
	return body, nil
}

func doAuthorized(client *http.Client, method, baseURL, path, token string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, strings.TrimRight(baseURL, "/")+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return client.Do(req)
}

func attachLocal(dbPath string, sessionID int64, detail attachDetail, stdin io.Reader, stdout, stderr io.Writer) error {
	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open local Rusui database: %w", err)
	}
	defer func() { _ = st.Close() }()
	payload, err := store.LatestPolicyPayload(st)
	if err != nil {
		return fmt.Errorf("read active policy revision: %w", err)
	}
	pol, err := policy.Parse(payload)
	if err != nil {
		return fmt.Errorf("parse active policy revision: %w", err)
	}
	eng := engine.New(st, pol, gh.NewAPI("", nil), clock.Real{})
	attach, stream, err := eng.AttachLocalSession(sessionID)
	if err != nil {
		return fmt.Errorf("local process is not attachable: %w", err)
	}
	defer func() { _ = stream.Close() }()
	process, err := store.LatestSumikaProcess(st, sessionID)
	if err != nil {
		return fmt.Errorf("read local process identity: %w", err)
	}
	detail.Processes = []store.Process{*process}
	if err := reportAttachIdentity(stderr, detail); err != nil {
		return fmt.Errorf("write session identity: %w", err)
	}
	path, err := sumika.DefaultSocketPath()
	if err != nil {
		return err
	}
	return attachLocalTerminal(stream, sumika.NewSocketClient(path), process.Name, stdin, stdout, stderr, attach.Generation)
}

func reportAttachIdentity(w io.Writer, detail attachDetail) error {
	runtimeName := "managed/" + detail.Session.Kind
	if detail.Session.Kind == store.SessionKindLocal {
		runtimeName = "local/sumika"
	}
	if _, err := fmt.Fprintf(w, "Project: %s | Session: %d | Environment: %d (%s) | Runtime: %s\n",
		emptyLabel(detail.Session.Project), detail.Session.ID, detail.Session.EnvironmentID, emptyLabel(detail.EnvironmentState), runtimeName); err != nil {
		return err
	}
	process := latestAttachProcess(detail.Processes)
	if process == nil || (process.State != store.ProcessRunning && process.State != store.ProcessIdle && process.State != store.ProcessBlocked) || process.CancelRequestedAt != nil {
		if _, err := fmt.Fprint(w, "Process: none live"); err != nil {
			return err
		}
		if process != nil {
			if _, err := fmt.Fprintf(w, " (latest generation %d is %s)", process.Generation, process.State); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(w, "Process: %d generation %d (%s)\n", process.ID, process.Generation, process.State); err != nil {
			return err
		}
	}
	if turn := latestAttachTurn(detail.Turns); turn != nil {
		_, err := fmt.Fprintf(w, "Turn: %d (%s)\n", turn.ID, turn.State)
		return err
	} else {
		_, err := fmt.Fprintln(w, "Turn: none")
		return err
	}
}

func latestAttachProcess(processes []store.Process) *store.Process {
	var latest *store.Process
	for i := range processes {
		if latest == nil || processes[i].Generation > latest.Generation {
			latest = &processes[i]
		}
	}
	return latest
}

func latestAttachTurn(turns []store.Turn) *store.Turn {
	var latest *store.Turn
	for i := range turns {
		if latest == nil || turns[i].ID > latest.ID {
			latest = &turns[i]
		}
	}
	return latest
}

func emptyLabel(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func attachLocalTerminal(stream io.ReadWriteCloser, client *sumika.SocketClient, name string, stdin io.Reader, stdout, stderr io.Writer, generation int64) (retErr error) {
	if _, err := fmt.Fprintf(stderr, "Attached locally as generation %d. Detach with Ctrl+].\n", generation); err != nil {
		return fmt.Errorf("write attach status: %w", err)
	}
	inputFile, isFile := stdin.(*os.File)
	terminal := isFile && term.IsTerminal(int(inputFile.Fd()))
	if terminal {
		state, err := term.MakeRaw(int(inputFile.Fd()))
		if err != nil {
			return fmt.Errorf("set terminal raw mode: %w", err)
		}
		defer func() {
			if err := term.Restore(int(inputFile.Fd()), state); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("restore terminal mode: %w", err))
			}
		}()
		if err := resizeLocalTerminal(client, name, int(inputFile.Fd())); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resizeErr := make(chan error, 1)
	if terminal {
		resizes := make(chan os.Signal, 1)
		signal.Notify(resizes, syscall.SIGWINCH)
		defer signal.Stop(resizes)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-resizes:
					if err := resizeLocalTerminal(client, name, int(inputFile.Fd())); err != nil {
						select {
						case resizeErr <- err:
						default:
						}
						cancel()
						return
					}
				}
			}
		}()
	}

	inputDone := make(chan error, 1)
	outputDone := make(chan error, 1)
	go func() { inputDone <- copyLocalInput(stream, stdin) }()
	go func() {
		_, err := io.Copy(stdout, stream)
		outputDone <- err
	}()
	select {
	case err := <-inputDone:
		select {
		case outputErr := <-outputDone:
			cancel()
			_ = stream.Close()
			if outputErr != nil && !errors.Is(outputErr, io.EOF) {
				return fmt.Errorf("local attach output: %w", outputErr)
			}
			return errors.New("local PTY disconnected; inspect the Session before reconnecting")
		default:
		}
		cancel()
		_ = stream.Close()
		if errors.Is(err, errDetach) || errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("local attach input: %w", err)
	case err := <-outputDone:
		cancel()
		_ = stream.Close()
		if err == nil || errors.Is(err, io.EOF) {
			return errors.New("local PTY disconnected; inspect the Session before reconnecting")
		}
		return fmt.Errorf("local attach output: %w", err)
	case err := <-resizeErr:
		cancel()
		_ = stream.Close()
		return err
	}
}

var errDetach = errors.New("detach requested")

func copyLocalInput(dst io.Writer, src io.Reader) error {
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			data := buf[:n]
			if i := bytes.IndexByte(data, 0x1d); i >= 0 {
				if i > 0 {
					if _, writeErr := dst.Write(data[:i]); writeErr != nil {
						return writeErr
					}
				}
				return errDetach
			}
			if _, writeErr := dst.Write(data); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			return err
		}
	}
}

func resizeLocalTerminal(client *sumika.SocketClient, name string, fd int) error {
	cols, rows, err := term.GetSize(fd)
	if err != nil {
		return fmt.Errorf("read terminal size: %w", err)
	}
	if err := client.Resize(name, cols, rows); err != nil {
		return fmt.Errorf("resize local PTY: %w", err)
	}
	return nil
}

func attachManaged(parent context.Context, client *http.Client, baseURL, token string, sessionID int64, page terminalPage, stdin io.Reader, stdout, stderr io.Writer) (retErr error) {
	if err := parent.Err(); err != nil {
		return err
	}
	write := false
	acquired := false
	leaseGeneration := 0
	if !page.leaseHeld {
		res, body, err := acquireManagedLease(client, baseURL, token, sessionID, page.csrf)
		if err != nil {
			return err
		}
		switch res.StatusCode {
		case http.StatusSeeOther:
			generation, err := strconv.Atoi(res.Header.Get(terminalGenerationHeader))
			if err != nil || generation < 1 {
				return errors.New("terminal lease response omitted its generation")
			}
			leaseGeneration, acquired, write = generation, true, true
			defer func() {
				if err := releaseManagedLease(client, baseURL, token, sessionID, page.csrf, leaseGeneration); err != nil {
					retErr = errors.Join(retErr, fmt.Errorf("release terminal lease generation %d: %w", leaseGeneration, err))
				}
			}()
			fresh, err := fetchOperatorPage(client, baseURL, token, fmt.Sprintf("/console/sessions/%d/terminal", sessionID))
			if err != nil {
				return err
			}
			page, err = parseTerminalPage(fresh)
			if err != nil {
				return err
			}
			if !page.leaseHeld || page.generation != leaseGeneration {
				return errors.New("terminal lease changed during attach")
			}
		case http.StatusConflict:
			if !strings.Contains(string(body), "write lease held") {
				return fmt.Errorf("acquire terminal lease: %s %s", res.Status, body)
			}
			fresh, err := fetchOperatorPage(client, baseURL, token, fmt.Sprintf("/console/sessions/%d/terminal", sessionID))
			if err != nil {
				return err
			}
			page, err = parseTerminalPage(fresh)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("acquire terminal lease: %s %s", res.Status, body)
		}
	}
	if !write {
		if _, err := fmt.Fprintln(stderr, "Managed terminal write lease is held; connected read-only. Press Ctrl+D to detach."); err != nil {
			return err
		}
	} else if acquired {
		if _, err := fmt.Fprintf(stderr, "Managed terminal write lease generation %d acquired.\n", leaseGeneration); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	outputDone := make(chan error, 1)
	inputDone := make(chan error, 1)
	go func() { outputDone <- streamManagedOutput(ctx, client, baseURL, token, sessionID, stdout) }()
	go func() {
		inputDone <- sendManagedInput(ctx, client, baseURL, token, sessionID, page.csrf, leaseGeneration, write, stdin, stderr)
	}()
	select {
	case err := <-inputDone:
		select {
		case outputErr := <-outputDone:
			cancel()
			if !errors.Is(outputErr, context.Canceled) {
				return errors.New("managed terminal disconnected; inspect the Environment before reconnecting")
			}
		default:
		}
		cancel()
		if err != nil {
			return err
		}
		if outputErr := <-outputDone; outputErr != nil && !errors.Is(outputErr, context.Canceled) {
			return fmt.Errorf("managed terminal output: %w", outputErr)
		}
		return nil
	case err := <-outputDone:
		cancel()
		if errors.Is(err, context.Canceled) {
			return nil
		}
		if err == nil || errors.Is(err, io.EOF) {
			return errors.New("managed terminal disconnected; inspect the Environment before reconnecting")
		}
		return fmt.Errorf("managed terminal output: %w", err)
	}
}

func acquireManagedLease(client *http.Client, baseURL, token string, sessionID int64, csrf string) (*http.Response, []byte, error) {
	values := url.Values{"csrf": {csrf}}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+fmt.Sprintf("/console/sessions/%d/terminal/lease", sessionID), strings.NewReader(values.Encode()))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := noRedirect.Do(req)
	if err != nil {
		return nil, nil, err
	}
	body, readErr := io.ReadAll(res.Body)
	_ = res.Body.Close()
	return res, body, readErr
}

func releaseManagedLease(client *http.Client, baseURL, token string, sessionID int64, csrf string, generation int) error {
	values := url.Values{"csrf": {csrf}, "generation": {strconv.Itoa(generation)}}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+fmt.Sprintf("/console/sessions/%d/terminal/revoke", sessionID), strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("%s %s", res.Status, body)
	}
	return nil
}

func sendManagedInput(ctx context.Context, client *http.Client, baseURL, token string, sessionID int64, csrf string, generation int, writable bool, stdin io.Reader, stderr io.Writer) error {
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	for scanner.Scan() {
		if !writable {
			continue
		}
		values := url.Values{"csrf": {csrf}, "generation": {strconv.Itoa(generation)}, "data": {scanner.Text()}}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+fmt.Sprintf("/console/sessions/%d/terminal/input", sessionID), strings.NewReader(values.Encode()))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		res, err := client.Do(req)
		if err != nil {
			return err
		}
		body, readErr := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if readErr != nil {
			return readErr
		}
		if res.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("managed terminal input: %s %s", res.Status, body)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !writable {
		if _, err := fmt.Fprintln(stderr, "Managed terminal detached read-only."); err != nil {
			return err
		}
	}
	return nil
}

func streamManagedOutput(ctx context.Context, client *http.Client, baseURL, token string, sessionID int64, stdout io.Writer) error {
	path := fmt.Sprintf("/console/sessions/%d/terminal/output", sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("managed terminal output: %s %s", res.Status, body)
	}
	scanner := bufio.NewScanner(res.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var data []string
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		event := strings.Join(data, "\n")
		data = data[:0]
		if event == "" {
			return nil // keepalive
		}
		var text string
		if err := json.Unmarshal([]byte(`"`+event+`"`), &text); err != nil {
			return fmt.Errorf("managed terminal output event: %w", err)
		}
		_, err := io.WriteString(stdout, text)
		return err
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			// Only the separator space is framing; the rest is terminal output.
			data = append(data, strings.TrimPrefix(value, " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}

func parseTerminalPage(body []byte) (terminalPage, error) {
	var page terminalPage
	csrf := csrfPattern.FindSubmatch(body)
	write := writePattern.FindSubmatch(body)
	gen := genPattern.FindSubmatch(body)
	if len(csrf) != 2 || len(write) != 2 || len(gen) != 2 {
		return page, errors.New("managed terminal page omitted its control state")
	}
	page.csrf = string(csrf[1])
	page.leaseHeld = string(write[1]) == "true"
	generation, err := strconv.Atoi(string(gen[1]))
	if err != nil {
		return page, err
	}
	page.generation = generation
	return page, nil
}

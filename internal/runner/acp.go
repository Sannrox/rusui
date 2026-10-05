package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/provider"
)

// ACPHost starts the ACP client for one claimed turn.
type ACPHost func(a *Assignment, dir string) (*acp.Client, func(), error)

// HTTPRecorder posts ACP receipts on the turn token. The runner has no store.
type HTTPRecorder struct {
	Base   string
	Token  string
	TurnID int64
	HTTP   *http.Client

	mu     sync.Mutex
	lastID string
}

func (r *HTTPRecorder) http() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return http.DefaultClient
}

func (r *HTTPRecorder) Record(rec acp.Receipt) error {
	payload, err := json.Marshal(map[string]any{
		"type":   rec.Type,
		"reason": rec.Reason,
		"body":   rec.Body,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/turns/%d/actions", r.Base, r.TurnID), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := r.http().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusAccepted {
		return fmt.Errorf("turn action: %s %s", res.Status, b)
	}
	var out struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b, &out)
	if rec.Type == acp.ActionApproval && out.ID != "" {
		r.mu.Lock()
		r.lastID = out.ID
		r.mu.Unlock()
	}
	return nil
}

func (r *HTTPRecorder) lastApprovalID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastID
}

func (r *HTTPRecorder) Wait(ctx context.Context, p acp.PermissionParams) acp.Decision {
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		id := r.lastApprovalID()
		if id != "" {
			d := r.poll(ctx, id)
			if d.Matched || d.Allow {
				return d
			}
			if r.denied(ctx, id) {
				return acp.Decision{}
			}
		}
		select {
		case <-ctx.Done():
			return acp.Decision{}
		case <-tick.C:
		}
	}
}

func (r *HTTPRecorder) denied(ctx context.Context, id string) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/approvals/%s", r.Base, id), nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+r.Token)
	res, err := r.http().Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = res.Body.Close() }()
	var out struct {
		Decision string `json:"decision"`
		Valid    bool   `json:"valid"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return out.Decision == "deny" || (out.Decision == "allow" && !out.Valid)
}

func (r *HTTPRecorder) poll(ctx context.Context, id string) acp.Decision {
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/approvals/%s", r.Base, id), nil)
	if err != nil {
		return acp.Decision{}
	}
	req.Header.Set("Authorization", "Bearer "+r.Token)
	res, err := r.http().Do(req)
	if err != nil {
		return acp.Decision{}
	}
	defer func() { _ = res.Body.Close() }()
	var out struct {
		Decision string `json:"decision"`
		Valid    bool   `json:"valid"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	if out.Decision == "allow" && out.Valid {
		return acp.Decision{Matched: true, Allow: true}
	}
	return acp.Decision{}
}

// GuestHost spawns the guest the plane named for the turn (Grok by default,
// ADR 0017 D1) and records receipts on the plane.
func GuestHost(c *Client) ACPHost {
	return func(a *Assignment, dir string) (*acp.Client, func(), error) {
		env := DriverEnv(a, dir, os.Getenv("PATH"))
		rec := &HTTPRecorder{Base: c.Base, Token: a.TurnToken, TurnID: a.TurnID, HTTP: c.HTTP}
		if a.Driver == "container" && a.Handle != "" {
			if c.Exec == nil {
				return nil, nil, fmt.Errorf("container exec required")
			}
			argv, err := acp.SpawnArgsFor(a.Guest)
			if err != nil {
				return nil, nil, err
			}
			stdin, stdout, stop, err := c.Exec.ExecStdio(a.Handle, argv, env)
			if err != nil {
				return nil, nil, err
			}
			return &acp.Client{In: stdout, Out: stdin, Rec: rec, Perm: permissionGate(a), Wait: rec.Wait}, stop, nil
		}
		cmd, err := acp.GuestCommand(a.Guest)
		if err != nil {
			return nil, nil, err
		}
		cmd.Dir = dir
		cmd.Env = env
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, nil, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, nil, err
		}
		stop := func() {
			_ = stdin.Close()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			_ = cmd.Wait()
		}
		return &acp.Client{In: stdout, Out: stdin, Rec: rec, Perm: permissionGate(a), Wait: rec.Wait}, stop, nil
	}
}

func permissionGate(a *Assignment) acp.PermissionGate {
	var gate acp.PermissionGate = acp.DenyUnmatched{}
	if a != nil && len(a.Permissions) > 0 {
		gate = acp.RulesGate{Rules: a.Permissions}
	}
	if a != nil && (a.GitHubToken != "" || a.Publication == planePublication) {
		// Implement sessions can publish (ADR 0015, ADR 0044); the
		// built-in fence applies whatever policy allows (ADR 0017 D3).
		gate = acp.FenceGate{Next: gate}
	}
	return gate
}

func WorkspaceFor(a *Assignment) (dir string, tmp bool, err error) {
	if a.Driver == "container" && a.Handle != "" && a.Workspace == "" {
		return "", false, nil
	}
	if a.Workspace != "" {
		return a.Workspace, false, nil
	}
	return "", true, nil
}

func OneACPTurn(ctx context.Context, c *Client, host ACPHost) error {
	_, err := OneACPTurnWithOutcome(ctx, c, host)
	return err
}

func OneACPTurnWithOutcome(ctx context.Context, c *Client, host ACPHost) (ClaimOutcome, error) {
	var outcome ClaimOutcome
	if host == nil {
		return outcome, fmt.Errorf("acp host required")
	}
	if err := c.Hello(); err != nil {
		return outcome, err
	}
	outcome, err := c.ClaimWithOutcome()
	if err != nil {
		return outcome, err
	}
	a := outcome.Assignment
	if a == nil {
		return outcome, nil
	}
	dir, tmp, err := WorkspaceFor(a)
	if err != nil {
		return outcome, err
	}
	cleanup := func() {}
	if tmp {
		dir, err = os.MkdirTemp("", "rusui-acp-*")
		if err != nil {
			return outcome, err
		}
		cleanup = func() { _ = os.RemoveAll(dir) }
	}
	defer cleanup()
	unhook, err := PrepareCommitHooks(c.Exec, a)
	if err != nil {
		_ = c.Fail(a)
		return outcome, err
	}
	defer unhook()
	unresult, err := PrepareResult(c.Exec, a)
	if err != nil {
		_ = c.Fail(a)
		return outcome, err
	}
	defer unresult()
	runCtx := ctx
	cancel := func() {}
	if a.ExecutionDeadline != nil {
		runCtx, cancel = context.WithDeadline(ctx, a.ExecutionDeadline.UTC())
	}
	defer cancel()
	runCtx, unlink, err := startTurnLink(runCtx, c, a)
	if err != nil {
		_ = c.Fail(a)
		return outcome, err
	}
	defer unlink()
	// A refused heartbeat ends the turn: a cancel that landed before the
	// harness started killed nothing in the guest (#472).
	runCtx, loseLease := context.WithCancelCause(runCtx)
	defer loseLease(nil)
	steers := make(chan engine.Steer, 1)
	stopHB := make(chan struct{})
	defer close(stopHB)
	go c.heartbeatSteers(runCtx, a, steers, stopHB, loseLease)
	if runCtx.Err() != nil {
		_ = c.Fail(a)
		return outcome, context.Cause(runCtx)
	}
	ac, stop, err := host(a, dir)
	if err != nil {
		_ = c.Fail(a)
		return outcome, err
	}
	defer stop()
	cwd := dir
	if cwd == "" && a.Driver == "container" && a.Handle != "" {
		cwd = env.WorkspaceDir // the guest runs in the container, not on the host
	}
	art, steerIDs, err := hostACP(runCtx, a, ac, cwd, steers, c.Exec)
	if cause := context.Cause(runCtx); errors.Is(cause, ErrLeaseLost) {
		err = cause
	}
	if err != nil {
		_ = c.Fail(a)
		return outcome, err
	}
	if a.ItemKind == "run" {
		art.Result = collectResult(c.Exec, a, cwd, art.SnapshotHash)
	}
	return outcome, c.CompleteWithSteers(a, art, steerIDs)
}

func HostACP(ctx context.Context, a *Assignment, host *acp.Client, cwd string) (engine.Artifact, error) {
	art, _, err := hostACP(ctx, a, host, cwd, nil, nil)
	return art, err
}

// heartbeatSteers renews the lease and delivers steers until stop. A
// heartbeat the plane refuses (ErrLeaseLost) is reported to lost and ends
// the loop; any other failure is retried on the next tick.
func (c *Client) heartbeatSteers(ctx context.Context, a *Assignment, steers chan<- engine.Steer, stop <-chan struct{}, lost func(error)) {
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	var queued []engine.Steer
	for {
		var send chan<- engine.Steer
		var value engine.Steer
		if len(queued) > 0 {
			send = steers
			value = queued[0]
		}
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case send <- value:
			queued = queued[1:]
		case <-tick.C:
			steer, err := c.PollHeartbeat(a)
			if errors.Is(err, ErrLeaseLost) {
				lost(err)
				return
			}
			if err != nil {
				continue
			}
			if steer != nil {
				queued = append(queued, *steer)
			}
		}
	}
}

func hostACP(ctx context.Context, a *Assignment, host *acp.Client, cwd string, steers <-chan engine.Steer, exec StdioExec) (engine.Artifact, []int64, error) {
	if a != nil && a.Guest == acp.GuestClaude {
		art, err := hostClaude(ctx, a, host, cwd)
		return art, nil, err
	}
	host.Ctx = ctx
	if _, err := host.Initialize(ctx); err != nil {
		return engine.Artifact{}, nil, err
	}
	sid := a.GuestSessionID
	if sid != "" {
		if err := host.SessionLoad(ctx, sid, cwd); err != nil {
			sid = ""
		}
	}
	if sid == "" {
		var err error
		sid, err = host.SessionNew(ctx, cwd)
		if err != nil {
			return engine.Artifact{}, nil, err
		}
	}
	prompt := promptFromInput(a.Input)
	if a.ResultPath != "" {
		prompt += resultInstructionsFor(a)
	}
	var currentSteerID int64
	var deliveredSteerIDs []int64
	type promptResponse struct {
		prompt *acp.PromptResult
		err    error
	}
	finish := func(res promptResponse) (engine.Artifact, []int64, error) {
		if res.err != nil {
			return engine.Artifact{}, nil, res.err
		}
		art := artifactFromAssignment(a, res.prompt)
		art.GuestSessionID = sid
		completedSteers := append([]int64(nil), deliveredSteerIDs...)
		if currentSteerID > 0 {
			completedSteers = append(completedSteers, currentSteerID)
		}
		return art, completedSteers, nil
	}
	for {
		result := make(chan promptResponse, 1)
		submitted := make(chan struct{})
		go func(text string) {
			pr, err := host.SessionPromptSubmitted(ctx, sid, text, submitted)
			result <- promptResponse{prompt: pr, err: err}
		}(prompt)
		select {
		case <-submitted:
			if currentSteerID > 0 {
				deliveredSteerIDs = append(deliveredSteerIDs, currentSteerID)
				currentSteerID = 0
			}
		case res := <-result:
			return finish(res)
		case <-ctx.Done():
			return engine.Artifact{}, nil, ctx.Err()
		}
		select {
		case res := <-result:
			return finish(res)
		default:
		}
		select {
		case res := <-result:
			return finish(res)
		case steer := <-steers:
			if err := host.SessionCancel(sid); err != nil {
				return engine.Artifact{}, nil, err
			}
			res := <-result
			if ctx.Err() != nil {
				return engine.Artifact{}, nil, ctx.Err()
			}
			if res.err != nil {
				return engine.Artifact{}, nil, res.err
			}
			if err := clearResult(exec, a, cwd); err != nil {
				return engine.Artifact{}, nil, err
			}
			prompt = steer.Prompt
			if a.ResultPath != "" {
				prompt += resultInstructionsFor(a)
			}
			currentSteerID = steer.ID
		}
	}
}

func promptFromInput(raw json.RawMessage) string {
	var in struct {
		Title                string `json:"title"`
		Body                 string `json:"body"`
		PublishedPullRequest int    `json:"published_pull_request"`
		PublishedSHA         string `json:"published_sha"`
	}
	_ = json.Unmarshal(raw, &in)
	var text string
	switch {
	case in.Title == "" && in.Body == "":
		text = "review this item"
	case in.Body == "":
		text = in.Title
	case in.Title == "":
		text = in.Body
	default:
		text = in.Title + "\n\n" + in.Body
	}
	if in.PublishedPullRequest > 0 {
		text += fmt.Sprintf("\n\nThis session already published pull request #%d", in.PublishedPullRequest)
		if in.PublishedSHA != "" {
			text += " at " + in.PublishedSHA
		}
		text += ". Update that pull request by committing and pushing onto its head. Do not open a second pull request. If you do not produce a new commit, write a blocked_reason instead of the same pull request."
	}
	return text
}

// hostClaude speaks the stream-json handshake (ADR 0025). It reads the
// Claude init event before writing a user message. ACP initialize is not sent.
// Resume is deferred on this host: the spawn has no --resume, so no Cursor
// is passed and every turn is a new conversation. The init session id is
// still recorded as the cursor. Provider events are not forwarded.
func hostClaude(ctx context.Context, a *Assignment, host *acp.Client, cwd string) (engine.Artifact, error) {
	if ctx.Err() != nil {
		return engine.Artifact{}, ctx.Err()
	}
	if host == nil {
		return engine.Artifact{}, fmt.Errorf("acp host required")
	}
	host.Ctx = ctx
	prompt := promptFromInput(a.Input)
	if a.ResultPath != "" {
		prompt += resultInstructionsFor(a)
	}
	rw := &stdioRWC{r: host.In, w: host.Out}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = rw.Close()
		case <-stop:
		}
	}()
	res, err := provider.Run(ctx, provider.KindClaude, provider.Instance{}, rw, provider.Turn{
		Prompt:    prompt,
		Workspace: cwd,
	}, claudeDecide(a, host))
	if err != nil {
		if ctx.Err() != nil {
			return engine.Artifact{}, ctx.Err()
		}
		return engine.Artifact{}, err
	}
	art := artifactFromAssignment(a, nil)
	art.GuestSessionID = res.Cursor
	return art, nil
}

func claudeDecide(a *Assignment, host *acp.Client) provider.Decide {
	return func(options []provider.Option, raw json.RawMessage) (string, bool) {
		params := acp.PermissionParams{ToolCall: raw, Options: make([]acp.PermOption, 0, len(options))}
		for _, o := range options {
			params.Options = append(params.Options, acp.PermOption{OptionID: o.ID})
		}
		if host != nil && host.Rec != nil {
			_ = host.Rec.Record(acp.Receipt{Type: acp.ActionPermission, Reason: acp.ReasonRecorded, Body: params})
		}
		d := permissionGate(a).Decide(params)
		if !d.Matched {
			if host != nil && host.Rec != nil {
				_ = host.Rec.Record(acp.Receipt{Type: acp.ActionApproval, Reason: acp.ReasonUnmatched, Body: params})
			}
			if host != nil && host.Wait != nil {
				d = host.Wait(ctxOrBackground(host.Ctx), params)
			}
		} else if !d.Allow && host != nil && host.Rec != nil {
			_ = host.Rec.Record(acp.Receipt{Type: acp.ActionApproval, Reason: acp.ReasonDenied, Body: params})
		}
		if d.Matched && d.Allow && len(options) > 0 {
			return options[0].ID, true
		}
		return "deny", false
	}
}

func ctxOrBackground(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

type stdioRWC struct {
	r io.Reader
	w io.Writer
	c sync.Once
}

func (s *stdioRWC) Read(p []byte) (int, error)  { return s.r.Read(p) }
func (s *stdioRWC) Write(p []byte) (int, error) { return s.w.Write(p) }
func (s *stdioRWC) Close() error {
	var err error
	s.c.Do(func() {
		if closer, ok := s.w.(io.Closer); ok {
			err = closer.Close()
		}
		if closer, ok := s.r.(io.Closer); ok {
			if e := closer.Close(); err == nil {
				err = e
			}
		}
	})
	return err
}

func artifactFromAssignment(a *Assignment, pr *acp.PromptResult) engine.Artifact {
	var in struct {
		MainSHA      string `json:"main_sha"`
		HeadSHA      string `json:"head_sha"`
		SnapshotHash string `json:"snapshot_hash"`
		ItemHash     string `json:"item_hash"`
	}
	_ = json.Unmarshal(a.Input, &in)
	hash := a.ItemHash
	if hash == "" {
		hash = in.ItemHash
	}
	if hash == "" {
		hash = in.SnapshotHash
	}
	stop := ""
	if pr != nil {
		stop = pr.StopReason
	}
	art := engine.Artifact{
		SchemaVersion:   1,
		Repo:            a.Repo,
		Item:            a.Item,
		ItemKind:        a.ItemKind,
		ClaimedRevision: a.ClaimedRevision,
		SnapshotHash:    hash,
		MainSHA:         in.MainSHA,
		HeadSHA:         in.HeadSHA,
		Verdict:         "keep",
		Confidence:      "low",
		Publishable:     map[string]any{"stop_reason": stop},
	}
	if a.ItemKind == "run" {
		// Stop reason is not a finding. Run turns fail closed unless the
		// agent reports a structured result (collectResult).
		art.Verdict = "blocked"
		art.Confidence = ""
		art.Result = nil
	}
	return art
}

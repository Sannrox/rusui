package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
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
	if a != nil && a.GitHubToken != "" {
		// Implement sessions hold a write credential (ADR 0015); the
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
	if host == nil {
		return fmt.Errorf("acp host required")
	}
	if err := c.Hello(); err != nil {
		return err
	}
	a, err := c.Claim()
	if err != nil {
		return err
	}
	if a == nil {
		return nil
	}
	dir, tmp, err := WorkspaceFor(a)
	if err != nil {
		return err
	}
	cleanup := func() {}
	if tmp {
		dir, err = os.MkdirTemp("", "rusui-acp-*")
		if err != nil {
			return err
		}
		cleanup = func() { _ = os.RemoveAll(dir) }
	}
	defer cleanup()
	unhook, err := PrepareCommitHooks(c.Exec, a)
	if err != nil {
		_ = c.Fail(a)
		return err
	}
	defer unhook()
	unresult, err := PrepareResult(c.Exec, a)
	if err != nil {
		_ = c.Fail(a)
		return err
	}
	defer unresult()
	runCtx := ctx
	cancel := func() {}
	if a.ExecutionDeadline != nil {
		runCtx, cancel = context.WithDeadline(ctx, a.ExecutionDeadline.UTC())
	}
	defer cancel()
	steers := make(chan engine.Steer, 1)
	stopHB := make(chan struct{})
	defer close(stopHB)
	go c.heartbeatSteers(runCtx, a, steers, stopHB)
	ac, stop, err := host(a, dir)
	if err != nil {
		_ = c.Fail(a)
		return err
	}
	defer stop()
	cwd := dir
	if cwd == "" && a.Driver == "container" && a.Handle != "" {
		cwd = env.WorkspaceDir // the guest runs in the container, not on the host
	}
	art, steerIDs, err := hostACP(runCtx, a, ac, cwd, steers, c.Exec)
	if err != nil {
		_ = c.Fail(a)
		return err
	}
	if a.ItemKind == "run" {
		art.Result = collectResult(c.Exec, a, cwd, art.SnapshotHash)
	}
	return c.CompleteWithSteers(a, art, steerIDs)
}

func HostACP(ctx context.Context, a *Assignment, host *acp.Client, cwd string) (engine.Artifact, error) {
	art, _, err := hostACP(ctx, a, host, cwd, nil, nil)
	return art, err
}

func (c *Client) heartbeatSteers(ctx context.Context, a *Assignment, steers chan<- engine.Steer, stop <-chan struct{}) {
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
		prompt += resultInstructions
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
				prompt += resultInstructions
			}
			currentSteerID = steer.ID
		}
	}
}

func promptFromInput(raw json.RawMessage) string {
	var in struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	_ = json.Unmarshal(raw, &in)
	if in.Title == "" && in.Body == "" {
		return "review this item"
	}
	if in.Body == "" {
		return in.Title
	}
	if in.Title == "" {
		return in.Body
	}
	return in.Title + "\n\n" + in.Body
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

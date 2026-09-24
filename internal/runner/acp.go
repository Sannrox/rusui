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
			d := r.poll(id)
			if d.Matched || d.Allow {
				return d
			}
			if r.denied(id) {
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

func (r *HTTPRecorder) denied(id string) bool {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/approvals/%s", r.Base, id), nil)
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

func (r *HTTPRecorder) poll(id string) acp.Decision {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/approvals/%s", r.Base, id), nil)
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
	stopHB := make(chan struct{})
	defer close(stopHB)
	go func() {
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stopHB:
				return
			case <-t.C:
				_ = c.Heartbeat(a)
			}
		}
	}()
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
	art, err := HostACP(runCtx, a, ac, cwd)
	if err != nil {
		_ = c.Fail(a)
		return err
	}
	if a.ItemKind == "run" {
		art.Result = collectResult(c.Exec, a, cwd, art.SnapshotHash)
	}
	return c.Complete(a, art)
}

func HostACP(ctx context.Context, a *Assignment, host *acp.Client, cwd string) (engine.Artifact, error) {
	host.Ctx = ctx
	if _, err := host.Initialize(ctx); err != nil {
		return engine.Artifact{}, err
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
			return engine.Artifact{}, err
		}
	}
	prompt := promptFromInput(a.Input)
	if a.ResultPath != "" {
		prompt += resultInstructions
	}
	pr, err := host.SessionPrompt(ctx, sid, prompt)
	if err != nil {
		return engine.Artifact{}, err
	}
	art := artifactFromAssignment(a, pr)
	art.GuestSessionID = sid
	return art, nil
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

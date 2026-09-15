package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/engine"
)

// ACPHost starts the ACP client for one claimed turn.
type ACPHost func(a *Assignment, dir string) (*acp.Client, func(), error)

// HTTPRecorder posts ACP receipts on the turn token. The runner has no store.
type HTTPRecorder struct {
	Base   string
	Token  string
	TurnID int64
	HTTP   *http.Client
}

func (r HTTPRecorder) http() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return http.DefaultClient
}

func (r HTTPRecorder) Record(rec acp.Receipt) error {
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
	if res.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("turn action: %s %s", res.Status, b)
	}
	return nil
}

// GrokHost spawns the ADR 0002 Grok command and records receipts on the plane.
func GrokHost(c *Client) ACPHost {
	return func(a *Assignment, dir string) (*acp.Client, func(), error) {
		env := DriverEnv(a, dir, os.Getenv("PATH"))
		rec := HTTPRecorder{Base: c.Base, Token: a.TurnToken, TurnID: a.TurnID, HTTP: c.HTTP}
		if a.Driver == "container" && a.Handle != "" {
			if c.Exec == nil {
				return nil, nil, fmt.Errorf("container exec required")
			}
			stdin, stdout, stop, err := c.Exec.ExecStdio(a.Handle, acp.SpawnArgs(), env)
			if err != nil {
				return nil, nil, err
			}
			return &acp.Client{In: stdout, Out: stdin, Rec: rec, Perm: acp.DenyUnmatched{}}, stop, nil
		}
		cmd, err := acp.GrokCommand()
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
		return &acp.Client{In: stdout, Out: stdin, Rec: rec, Perm: acp.DenyUnmatched{}}, stop, nil
	}
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
	art, err := HostACP(runCtx, a, ac, dir)
	if err != nil {
		_ = c.Fail(a)
		return err
	}
	return c.Complete(a, art)
}

func HostACP(ctx context.Context, a *Assignment, host *acp.Client, cwd string) (engine.Artifact, error) {
	if _, err := host.Initialize(ctx); err != nil {
		return engine.Artifact{}, err
	}
	sid, err := host.SessionNew(ctx, cwd)
	if err != nil {
		return engine.Artifact{}, err
	}
	pr, err := host.SessionPrompt(ctx, sid, promptFromInput(a.Input))
	if err != nil {
		return engine.Artifact{}, err
	}
	return artifactFromAssignment(a, pr), nil
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
	return engine.Artifact{
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
}

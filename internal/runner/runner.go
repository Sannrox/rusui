package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/engine"
)

// Client talks outbound-only to the plane. Bootstrap authenticates
// register and claim. The driver process never receives Bootstrap.
type Client struct {
	Base      string
	Bootstrap string
	Repo      string
	Name      string
	HTTP      *http.Client
	Exec      StdioExec
}

// StdioExec runs a command inside a container handle with ACP stdio.
type StdioExec interface {
	ExecStdio(handle string, argv, env []string) (stdin io.WriteCloser, stdout io.ReadCloser, stop func(), err error)
}

type Assignment struct {
	TurnID            int64           `json:"turn_id"`
	JobID             int64           `json:"job_id"`
	LeaseGeneration   int             `json:"lease_generation"`
	ClaimedRevision   int             `json:"claimed_revision"`
	Repo              string          `json:"repo"`
	Item              int             `json:"item"`
	ItemKind          string          `json:"item_kind"`
	ItemHash          string          `json:"item_hash"`
	SessionID         int64           `json:"session_id"`
	TurnToken         string          `json:"turn_token"`
	Driver            string          `json:"driver"`
	Handle            string          `json:"handle"`
	Workspace         string          `json:"workspace"`
	ModelBaseURL      string          `json:"model_base_url"`
	GitProxyURL       string          `json:"git_proxy_url"`
	GitHubToken       string          `json:"github_token,omitempty"`
	Permissions       []acp.Rule      `json:"permissions"`
	ExecutionDeadline *time.Time      `json:"execution_deadline"`
	Input             json.RawMessage `json:"input"`
	GuestSessionID    string          `json:"guest_session_id,omitempty"`
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) Hello() error {
	body, _ := json.Marshal(map[string]string{"name": c.Name})
	req, err := http.NewRequest("POST", c.Base+"/runners/hello", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Bootstrap)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("hello: %s %s", res.Status, b)
	}
	return nil
}

func (c *Client) Claim() (*Assignment, error) {
	body, _ := json.Marshal(map[string]string{"repo": c.Repo})
	req, err := http.NewRequest("POST", c.Base+"/jobs/claim", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Bootstrap)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == 204 {
		return nil, nil
	}
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("claim: %s %s", res.Status, b)
	}
	var a Assignment
	if err := json.NewDecoder(res.Body).Decode(&a); err != nil {
		return nil, err
	}
	if a.TurnID == 0 {
		a.TurnID = a.JobID
	}
	return &a, nil
}

func (c *Client) turnReq(method, path string, a *Assignment, payload any) error {
	var rdr io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.Base+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.TurnToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("%s: %s %s", path, res.Status, b)
	}
	return nil
}

func (c *Client) Heartbeat(a *Assignment) error {
	return c.turnReq("POST", fmt.Sprintf("/jobs/%d/heartbeat", a.TurnID), a, map[string]int{
		"lease_generation": a.LeaseGeneration,
		"claimed_revision": a.ClaimedRevision,
	})
}

func (c *Client) Complete(a *Assignment, art engine.Artifact) error {
	return c.turnReq("POST", fmt.Sprintf("/jobs/%d/complete", a.TurnID), a, map[string]any{
		"lease_generation": a.LeaseGeneration,
		"claimed_revision": a.ClaimedRevision,
		"artifact":         art,
	})
}

func (c *Client) Fail(a *Assignment) error {
	return c.turnReq("POST", fmt.Sprintf("/jobs/%d/fail", a.TurnID), a, map[string]int{
		"lease_generation": a.LeaseGeneration,
		"claimed_revision": a.ClaimedRevision,
	})
}

// DriverEnv is the only environment the process driver may see.
func DriverEnv(a *Assignment, home, path string) []string {
	env := []string{
		"PATH=" + path,
		"HOME=" + home,
		"RUSUI_TURN_TOKEN=" + a.TurnToken,
		"RUSUI_TURN_ID=" + fmt.Sprint(a.TurnID),
		"XAI_API_KEY=" + a.TurnToken,
	}
	if a.ModelBaseURL != "" {
		env = append(env, "GROK_XAI_API_BASE_URL="+a.ModelBaseURL)
	}
	if a.GitHubToken != "" {
		// Implement session (ADR 0015): the agent talks to GitHub directly as
		// the operator, so git bypasses the proxy grant.
		return append(env,
			"GH_TOKEN="+a.GitHubToken,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=credential.https://github.com.helper",
			`GIT_CONFIG_VALUE_0=!f() { test "$1" = get && echo username=x-access-token && echo "password=$GH_TOKEN"; }; f`,
		)
	}
	if a.GitProxyURL != "" {
		env = append(env,
			"GIT_CONFIG_COUNT=2",
			"GIT_CONFIG_KEY_0=url."+a.GitProxyURL+".insteadof",
			"GIT_CONFIG_VALUE_0=https://github.com/",
			"GIT_CONFIG_KEY_1=http.extraHeader",
			"GIT_CONFIG_VALUE_1=Authorization: Bearer "+a.TurnToken,
		)
	}
	return env
}

func RunProcess(ctx context.Context, command []string, env []string, dir string) ([]byte, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("empty driver command")
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		<-done
		return nil, ctx.Err()
	case err := <-done:
		if err != nil {
			return nil, fmt.Errorf("%w: %s", err, stderr.String())
		}
		return stdout.Bytes(), nil
	}
}

func OneTurn(ctx context.Context, c *Client, command []string) error {
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
	dir, err := os.MkdirTemp("", "rusui-turn-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	env := DriverEnv(a, dir, os.Getenv("PATH"))
	for _, e := range env {
		if strings.HasPrefix(e, "RUSUI_WORKER_SECRET=") || strings.HasPrefix(e, "RUSUI_SLACK_SECRET=") || strings.HasPrefix(e, "RUSUI_WEBHOOK_SECRET=") {
			return fmt.Errorf("plane secret leaked into driver env")
		}
	}
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
	out, err := RunProcess(runCtx, command, env, dir)
	if err != nil {
		_ = c.Fail(a)
		return err
	}
	var art engine.Artifact
	if err := json.Unmarshal(out, &art); err != nil {
		_ = c.Fail(a)
		return fmt.Errorf("driver artifact: %w", err)
	}
	return c.Complete(a, art)
}

func DriverScript(dir, artifactJSON string) (string, error) {
	out := filepath.Join(dir, "artifact.json")
	if err := os.WriteFile(out, []byte(artifactJSON), 0o600); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "driver.sh")
	body := "#!/bin/sh\ncat \"$0.json\"\n"
	if err := os.WriteFile(p, []byte(body), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(p+".json", []byte(artifactJSON), 0o600); err != nil {
		return "", err
	}
	return p, nil
}

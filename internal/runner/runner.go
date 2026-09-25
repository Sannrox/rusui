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
	rusuienv "github.com/sannrox/rusui/internal/env"
)

// Client talks outbound-only to the plane. Bootstrap authenticates
// register and claim. The driver process never receives Bootstrap.
type Client struct {
	Base      string
	Bootstrap string
	Repo      string // legacy single-repository configuration
	Repos     []string
	Name      string
	HTTP      *http.Client
	Exec      StdioExec
	nextRepo  int
}

// StdioExec runs a command inside a container handle with ACP stdio.
type StdioExec interface {
	ExecStdio(handle string, argv, env []string) (stdin io.WriteCloser, stdout io.ReadCloser, stop func(), err error)
}

type Assignment struct {
	TurnID          int64    `json:"turn_id"`
	JobID           int64    `json:"job_id"`
	LeaseGeneration int      `json:"lease_generation"`
	ClaimedRevision int      `json:"claimed_revision"`
	Repo            string   `json:"repo"`
	Item            int      `json:"item"`
	ItemKind        string   `json:"item_kind"`
	ItemHash        string   `json:"item_hash"`
	SessionID       int64    `json:"session_id"`
	TurnToken       string   `json:"turn_token"`
	Driver          string   `json:"driver"`
	Handle          string   `json:"handle"`
	Workspace       string   `json:"workspace"`
	ModelBaseURL    string   `json:"model_base_url"`
	GitProxyURL     string   `json:"git_proxy_url"`
	GitHubToken     string   `json:"github_token,omitempty"`
	Guest           string   `json:"guest,omitempty"`
	CommitTrailers  []string `json:"commit_trailers,omitempty"`
	// CommitHooksDir is where PrepareCommitHooks placed the attribution
	// hooks for this turn; set on the runner, never by the plane.
	CommitHooksDir string `json:"-"`
	// ResultPath is where a run turn's guest writes its structured result
	// (PrepareResult).
	ResultPath        string          `json:"-"`
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
	repos, err := c.claimRepos()
	if err != nil {
		return nil, err
	}
	if len(repos) == 1 {
		a, _, err := c.claimRepo(repos[0])
		return a, err
	}
	start := c.nextRepo % len(repos)
	c.nextRepo = (start + 1) % len(repos)
	for offset := range len(repos) {
		repo := repos[(start+offset)%len(repos)]
		a, blocked, err := c.claimRepo(repo)
		if err != nil {
			if blocked {
				continue
			}
			return nil, err
		}
		if a != nil {
			return a, nil
		}
	}
	return nil, nil
}

func (c *Client) claimRepos() ([]string, error) {
	if c.Repo != "" && len(c.Repos) != 0 {
		return nil, fmt.Errorf("claim: configure Repo or Repos, not both")
	}
	repos := append([]string(nil), c.Repos...)
	if len(repos) == 0 && c.Repo != "" {
		repos = []string{c.Repo}
	}
	if len(repos) == 0 {
		return nil, fmt.Errorf("claim: at least one repository must be configured")
	}
	seen := make(map[string]struct{}, len(repos))
	for i, repo := range repos {
		repo = strings.TrimSpace(repo)
		if repo == "" {
			return nil, fmt.Errorf("claim: repository must not be empty")
		}
		key := strings.ToLower(repo)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("claim: repository %q was listed more than once", repo)
		}
		seen[key] = struct{}{}
		repos[i] = repo
	}
	return repos, nil
}

func (c *Client) claimRepo(repo string) (*Assignment, bool, error) {
	body, _ := json.Marshal(map[string]string{"repo": repo})
	req, err := http.NewRequest("POST", c.Base+"/jobs/claim", bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Bootstrap)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http().Do(req)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == 204 {
		return nil, false, nil
	}
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		blocked := res.Header.Get("X-Rusui-Claim-Blocked")
		return nil, blocked == "budget" || blocked == "paused", fmt.Errorf("claim: %s %s", res.Status, b)
	}
	var a Assignment
	if err := json.NewDecoder(res.Body).Decode(&a); err != nil {
		return nil, false, err
	}
	if a.TurnID == 0 {
		a.TurnID = a.JobID
	}
	if !strings.EqualFold(a.Repo, repo) {
		return nil, false, fmt.Errorf("claim: plane returned repository %q for configured repository %q", a.Repo, repo)
	}
	return &a, false, nil
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
	}
	if a.Guest == acp.GuestClaude {
		// Claude Code talks to the plane model proxy with the grant; its
		// config dir is fresh, so no operator login is found (ADR 0017 D1).
		configDir := "/tmp/rusui-claude"
		if home != "" {
			configDir = home + "/.rusui-claude"
		}
		env = append(env,
			"ANTHROPIC_AUTH_TOKEN="+a.TurnToken,
			"CLAUDE_CONFIG_DIR="+configDir,
			"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
			"DISABLE_TELEMETRY=1",
		)
		if a.ModelBaseURL != "" {
			env = append(env, "ANTHROPIC_BASE_URL="+a.ModelBaseURL)
		}
	} else {
		env = append(env, "XAI_API_KEY="+a.TurnToken)
		if a.ModelBaseURL != "" {
			env = append(env, "GROK_XAI_API_BASE_URL="+a.ModelBaseURL)
		}
	}
	if a.ResultPath != "" {
		env = append(env, "RUSUI_RESULT="+a.ResultPath)
	}
	var git [][2]string
	if a.GitHubToken != "" {
		// Implement session (ADR 0015): the agent talks to GitHub directly as
		// the operator, so git bypasses the proxy grant.
		env = append(env, "GH_TOKEN="+a.GitHubToken)
		git = append(git, [2]string{"credential.https://github.com.helper",
			`!f() { test "$1" = get && echo username=x-access-token && echo "password=$GH_TOKEN"; }; f`})
	} else if a.GitProxyURL != "" {
		git = append(git,
			[2]string{"url." + a.GitProxyURL + ".insteadof", "https://github.com/"},
			[2]string{"http.extraHeader", "Authorization: Bearer " + a.TurnToken},
		)
	}
	if a.CommitHooksDir != "" {
		env = append(env, "RUSUI_COMMIT_TRAILERS="+strings.Join(a.CommitTrailers, "\n"))
		git = append(git, [2]string{"core.hooksPath", a.CommitHooksDir})
	}
	if a.Driver == "container" && a.Handle != "" {
		// The tree is copied in with another owner; trust only the
		// workspace so agents need no safe.directory workaround.
		git = append(git, [2]string{"safe.directory", rusuienv.WorkspaceDir})
	}
	if len(git) > 0 {
		env = append(env, "GIT_CONFIG_PARAMETERS="+gitConfigParameters(git))
	}
	return env
}

// gitConfigParameters encodes settings the way git passes `git -c` values
// to its children. They keep command-line precedence but, unlike
// GIT_CONFIG_COUNT, are not replaced when an agent exports its own
// GIT_CONFIG_COUNT/KEY/VALUE settings.
func gitConfigParameters(kvs [][2]string) string {
	parts := make([]string, len(kvs))
	for i, kv := range kvs {
		parts[i] = sqQuote(kv[0]) + "=" + sqQuote(kv[1])
	}
	return strings.Join(parts, " ")
}

// sqQuote single-quotes s as git's sq_quote_buf does.
func sqQuote(s string) string {
	r := strings.NewReplacer("'", `'\''`, "!", `'\!'`)
	return "'" + r.Replace(s) + "'"
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
	unhook, err := PrepareCommitHooks(c.Exec, a)
	if err != nil {
		_ = c.Fail(a)
		return err
	}
	defer unhook()
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

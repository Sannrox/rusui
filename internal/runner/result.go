package runner

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sannrox/rusui/internal/engine"
	rusuienv "github.com/sannrox/rusui/internal/env"
)

// GuestResultPath is where a container guest writes its turn result.
const GuestResultPath = "/tmp/rusui-result.json"

// resultInstructions tells a run-turn agent how to report its outcome.
const resultInstructions = "\n\nWhen you finish, write one JSON object to the file named by $RUSUI_RESULT: " +
	`{"pull_request": <number>} if you opened or updated a pull request, otherwise {"blocked_reason": "<why you stopped>"}.`

// maxResultBytes bounds what the runner reads back from the guest.
const maxResultBytes = 64 << 10

// PrepareResult gives a run turn an empty file path for its structured
// result and records it on the assignment. The returned cleanup removes a
// host directory.
func PrepareResult(x StdioExec, a *Assignment) (func(), error) {
	if a.ItemKind != "run" {
		return func() {}, nil
	}
	if a.Driver == "container" && a.Handle != "" {
		a.ResultPath = GuestResultPath
		// The container outlives the turn; drop an earlier turn's result.
		_, _ = guestOutput(x, a, "", "rm", "-f", GuestResultPath)
		return func() {}, nil
	}
	dir, err := os.MkdirTemp("", "rusui-result-*")
	if err != nil {
		return nil, err
	}
	a.ResultPath = filepath.Join(dir, "result.json")
	return func() { _ = os.RemoveAll(dir) }, nil
}

func clearResult(x StdioExec, a *Assignment, cwd string) error {
	if a.ResultPath == "" {
		return nil
	}
	if a.Driver == "container" && a.Handle != "" {
		_, err := guestOutput(x, a, cwd, "rm", "-f", a.ResultPath)
		return err
	}
	return os.WriteFile(a.ResultPath, nil, 0o600)
}

// collectResult builds a run result from the agent's structured report and
// the workspace HEAD the runner observes itself. Without a report it
// returns nil, and the turn fails closed. The plane checks any claimed pull
// request against GitHub (engine.observeResult).
func collectResult(x StdioExec, a *Assignment, cwd, sourceHash string) *engine.TaskResult {
	if a.ResultPath == "" {
		return nil
	}
	raw, err := guestOutput(x, a, "", "cat", a.ResultPath)
	if err != nil {
		return nil
	}
	var claim struct {
		PullRequest   int    `json:"pull_request"`
		BlockedReason string `json:"blocked_reason"`
	}
	if json.Unmarshal(raw, &claim) != nil {
		return nil
	}
	claim.BlockedReason = strings.TrimSpace(claim.BlockedReason)
	if claim.PullRequest <= 0 && claim.BlockedReason == "" {
		return nil
	}
	git := []string{"git"}
	if a.Driver == "container" && a.Handle != "" {
		// Same ownership mismatch as the guest's own git (DriverEnv).
		git = append(git, "-c", "safe.directory="+rusuienv.WorkspaceDir)
	}
	head, _ := guestOutput(x, a, cwd, append(git, "rev-parse", "--verify", "HEAD")...)
	return &engine.TaskResult{
		SchemaVersion: engine.ResultSchema,
		SourceHash:    sourceHash,
		CandidateSHA:  strings.TrimSpace(string(head)),
		PullRequest:   max(claim.PullRequest, 0),
		BlockedReason: claim.BlockedReason,
	}
}

// guestOutput runs argv where the guest runs: inside its container (in the
// workspace), or on the host in dir.
func guestOutput(x StdioExec, a *Assignment, dir string, argv ...string) ([]byte, error) {
	if a.Driver == "container" && a.Handle != "" {
		if x == nil {
			return nil, os.ErrInvalid
		}
		stdin, stdout, stop, err := x.ExecStdio(a.Handle, argv, nil)
		if err != nil {
			return nil, err
		}
		defer stop()
		_ = stdin.Close()
		return io.ReadAll(io.LimitReader(stdout, maxResultBytes))
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if len(out) > maxResultBytes {
		out = out[:maxResultBytes]
	}
	return out, err
}

package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// A guest that commits from a nested worktree leaves the workspace HEAD at
// the base; the result still names that branch tip (#469).
func TestCollectResultReportsNestedWorktreeBranch(t *testing.T) {
	ws := t.TempDir()
	gitIn(t, ws, "init", "-q", "-b", "main")
	gitIn(t, ws, "commit", "-q", "--allow-empty", "-m", "base")
	base := gitIn(t, ws, "rev-parse", "HEAD")
	wt := filepath.Join(ws, ".claude", "worktrees", "fix")
	gitIn(t, ws, "worktree", "add", "-q", "-b", "fix/nested", wt)
	gitIn(t, wt, "commit", "-q", "--allow-empty", "-m", "fix")
	fix := gitIn(t, wt, "rev-parse", "HEAD")

	resultPath := filepath.Join(t.TempDir(), "result.json")
	if err := os.WriteFile(resultPath, []byte(`{"pull_request": 9}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := collectResult(nil, &Assignment{ResultPath: resultPath}, ws, "src")
	if r == nil {
		t.Fatal("no result")
	}
	if r.CandidateSHA != base {
		t.Fatalf("candidate %s, want workspace HEAD %s", r.CandidateSHA, base)
	}
	if !slices.Contains(r.BranchHeads, fix) || !slices.Contains(r.BranchHeads, base) {
		t.Fatalf("branch heads %v, want %s and %s", r.BranchHeads, base, fix)
	}
}

func TestBranchHeadsDedupesAndBounds(t *testing.T) {
	sha := func(c byte) string { return strings.Repeat(string(c), 40) }
	var lines []string
	for i := range maxBranchHeads + 10 {
		lines = append(lines, sha(byte('a'+i%26))+strings.Repeat("0", i/26))
	}
	lines = append(lines, sha('a'), "", "short")
	got := branchHeads([]byte(strings.Join(lines, "\n")))
	if len(got) != maxBranchHeads {
		t.Fatalf("kept %d heads, want %d", len(got), maxBranchHeads)
	}
	if slices.Contains(got, "short") || slices.Contains(got, "") {
		t.Fatalf("kept a non-commit line: %v", got)
	}
}

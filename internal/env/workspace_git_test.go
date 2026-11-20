package env

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitDiffReportsDirtCleanAndPinless(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, state, err := GitDiff(dir, 1<<20)
	if err != nil || state != FileNoGitPin || len(body) != 0 {
		t.Fatalf("pinless body %q state %q err %v", body, state, err)
	}

	for _, args := range [][]string{
		{"-c", "init.defaultBranch=main", "init", "-q"},
		{"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "pin"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	body, state, err = GitDiff(dir, 1<<20)
	if err != nil || state != FileClean || len(body) != 0 {
		t.Fatalf("clean body %q state %q err %v", body, state, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello-guest\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, state, err = GitDiff(dir, 1<<20)
	if err != nil || state != "" {
		t.Fatalf("dirt state %q err %v", state, err)
	}
	got := string(body)
	if strings.Contains(got, "--- /dev/null") || !strings.Contains(got, "note.txt") || !strings.Contains(got, "-hello") || !strings.Contains(got, "+hello-guest") {
		t.Fatalf("dirt %q", got)
	}

	capped, capState, err := GitDiff(dir, 8)
	if err != nil || capState != "" {
		t.Fatalf("cap state %q err %v", capState, err)
	}
	if int64(len(capped)) <= 8 || !strings.Contains(string(capped), FileSkipped) {
		t.Fatalf("capped %q", capped)
	}
}

func TestDockerCLIWorkspaceGitDiffStates(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "docker")
	script := "#!/bin/sh\n" +
		"if [ -n \"$STUB_EXIT\" ]; then exit \"$STUB_EXIT\"; fi\n" +
		"if [ -n \"$STUB_BODY\" ]; then printf '%s' \"$STUB_BODY\"; exit 0; fi\n" +
		"printf ' M note.txt\\n\\n--- a/note.txt\\n+++ b/note.txt\\n-hello\\n+hello-guest\\n'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	d := DockerCLI{Bin: bin}
	body, state, err := d.WorkspaceGitDiff("ctr", 1<<20)
	if err != nil || state != "" || !strings.Contains(string(body), "note.txt") || strings.Contains(string(body), "--- /dev/null") {
		t.Fatalf("dirt body %q state %q err %v", body, state, err)
	}

	t.Setenv("STUB_BODY", "\n")
	body, state, err = d.WorkspaceGitDiff("ctr", 1<<20)
	if err != nil || state != FileClean || len(body) != 0 {
		t.Fatalf("clean body %q state %q err %v", body, state, err)
	}

	t.Setenv("STUB_EXIT", "14")
	body, state, err = d.WorkspaceGitDiff("ctr", 1<<20)
	if err != nil || state != FileNoGitPin || len(body) != 0 {
		t.Fatalf("pinless body %q state %q err %v", body, state, err)
	}

	t.Setenv("STUB_EXIT", "13")
	_, _, err = d.WorkspaceGitDiff("ctr", 1<<20)
	if !errors.Is(err, ErrWorkspaceReplaced) {
		t.Fatalf("replaced err %v", err)
	}
}

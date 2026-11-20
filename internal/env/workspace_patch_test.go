package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyGitPatchEditsFileAndRefusesConflict(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ApplyGitPatch(dir, unifiedPatch("note.txt", "hello\n", "hello-guest\n")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "note.txt"))
	if err != nil || string(got) != "hello-guest\n" {
		t.Fatalf("got %q %v", got, err)
	}

	if err := ApplyGitPatch(dir, unifiedPatch("missing.txt", "old\n", "new\n")); err == nil {
		t.Fatal("expected apply failure")
	}
	got, err = os.ReadFile(filepath.Join(dir, "note.txt"))
	if err != nil || string(got) != "hello-guest\n" {
		t.Fatalf("conflict mutated tree %q %v", got, err)
	}
}

func TestFakeRuntimeApplyPatch(t *testing.T) {
	t.Parallel()
	f := &FakeRuntime{}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "README"), []byte("pin-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.PlaceTree("ctr", src); err != nil {
		t.Fatal(err)
	}
	if err := f.ApplyPatch("ctr", unifiedPatch("README", "pin-a\n", "patched\n")); err != nil {
		t.Fatal(err)
	}
	got, err := f.ReadFile("ctr", "README")
	if err != nil || string(got) != "patched\n" {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestDockerCLIApplyPatchKeepsPatchOffArgv(t *testing.T) {
	bin, logPath := stubDocker(t)
	d := DockerCLI{Bin: bin}
	const patch = "not-on-argv-patch-bytes"
	if err := d.ApplyPatch("0123456789abcdef", []byte(patch)); err != nil {
		t.Fatal(err)
	}
	logb, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logb)
	if strings.Contains(log, patch) {
		t.Fatalf("patch on argv: %s", log)
	}
	prefix := "exec -i -w " + workspaceDir + " 0123456789abcdef git -c safe.directory=" + workspaceDir + " apply"
	var sawCheck, sawApply bool
	for line := range strings.SplitSeq(log, "\n") {
		switch strings.TrimSpace(line) {
		case prefix + " --check":
			sawCheck = true
		case prefix:
			sawApply = true
		}
	}
	if !sawCheck || !sawApply {
		t.Fatalf("check=%v apply=%v log %s", sawCheck, sawApply, log)
	}
}

func unifiedPatch(path, old, new string) []byte {
	return []byte("--- a/" + path + "\n+++ b/" + path + "\n@@ -1 +1 @@\n-" + strings.TrimSuffix(old, "\n") + "\n+" + strings.TrimSuffix(new, "\n") + "\n")
}

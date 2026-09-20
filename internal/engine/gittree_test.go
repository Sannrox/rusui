package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitFetcherUsesPrepareGrantNotPAT(t *testing.T) {
	bin, logPath := stubGit(t)
	dest := t.TempDir()
	g := GitFetcher{
		Git:      bin,
		Token:    "plane-tok",
		ProxyURL: "http://127.0.0.1:8080/git-proxy/github.com/",
		Grant:    "prep-grant",
	}
	if err := g.Fetch("example/test-repo", "abc", dest); err != nil {
		t.Fatal(err)
	}
	logb, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logb)
	if !strings.Contains(log, "http://127.0.0.1:8080/git-proxy/github.com/example/test-repo.git") {
		t.Fatalf("proxy origin missing: %s", log)
	}
	if !strings.Contains(log, "Bearer prep-grant") {
		t.Fatalf("grant missing: %s", log)
	}
	if strings.Contains(log, "plane-tok") {
		t.Fatalf("plane token used: %s", log)
	}
	if err := filepath.WalkDir(dest, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), "prep-grant") || strings.Contains(string(b), "plane-tok") {
			return fmtWalk(path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestGitFetcherProxyRequiresGrant(t *testing.T) {
	bin, _ := stubGit(t)
	g := GitFetcher{Git: bin, ProxyURL: "http://127.0.0.1:8080/git-proxy/github.com/"}
	if err := g.Fetch("example/test-repo", "abc", t.TempDir()); err == nil {
		t.Fatal("expected missing grant")
	}
}

func stubGit(t *testing.T) (bin, logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "log")
	bin = filepath.Join(dir, "git")
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> \"$GIT_STUB_LOG\"\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_STUB_LOG", logPath)
	return bin, logPath
}

type walkErr string

func (e walkErr) Error() string { return string(e) }

func fmtWalk(path string) error { return walkErr("secret in " + path) }

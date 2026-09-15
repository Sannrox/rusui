package engine

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// GitFetcher clones a pin on the host. Token is used only as an HTTP header
// for fetch; it is never written into dest.
type GitFetcher struct {
	Token   string
	TokenFn func() (string, error)
	Git     string
}

func (g GitFetcher) token() string {
	if g.TokenFn != nil {
		if t, err := g.TokenFn(); err == nil && t != "" {
			return t
		}
	}
	return g.Token
}

func (g GitFetcher) Fetch(repo, pin, dest string) error {
	if repo == "" || pin == "" || dest == "" {
		return fmt.Errorf("git: repo, pin, and dest required")
	}
	if strings.Contains(repo, "..") || strings.ContainsAny(repo, " \t\n") {
		return fmt.Errorf("git: invalid repo %q", repo)
	}
	bin := g.Git
	if bin == "" {
		bin = "git"
	}
	origin := "https://github.com/" + repo + ".git"
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	if err := gitDir(bin, dest, nil, "init"); err != nil {
		return err
	}
	if err := gitDir(bin, dest, nil, "remote", "add", "origin", origin); err != nil {
		return err
	}
	fetch := []string{"fetch", "--depth", "1", "origin", pin}
	var env []string
	if tok := g.token(); tok != "" {
		env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		fetch = []string{"-c", "http.extraHeader=Authorization: Bearer " + tok, "fetch", "--depth", "1", "origin", pin}
	}
	if err := gitDir(bin, dest, env, fetch...); err != nil {
		return err
	}
	if err := gitDir(bin, dest, nil, "checkout", "--force", "FETCH_HEAD"); err != nil {
		return err
	}
	return gitDir(bin, dest, nil, "remote", "set-url", "origin", origin)
}

func gitDir(bin, dir string, env []string, args ...string) error {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	if err := cmd.Run(); err != nil {
		name := "git"
		if len(args) > 0 {
			name = args[0]
			if name == "-c" && len(args) > 2 {
				name = args[2]
			}
		}
		return fmt.Errorf("git %s: %w", name, err)
	}
	return nil
}

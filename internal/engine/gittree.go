package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitFetcher clones a pin on the host. A ProxyURL plus prepare grant is the
// dogfood path: the GitHub token never enters dest. Token/TokenFn remain for
// process-driver tests that talk to GitHub directly.
type GitFetcher struct {
	Token    string
	TokenFn  func() (string, error)
	ProxyURL string
	Grant    string
	GrantFn  func(repo string) (string, error)
	CAFile   string
	Git      string
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
	auth := ""
	if g.ProxyURL != "" {
		origin = strings.TrimRight(g.ProxyURL, "/") + "/" + repo + ".git"
		if strings.HasPrefix(origin, "https://") && g.CAFile == "" {
			return fmt.Errorf("git: plane CA required for https proxy")
		}
		auth = g.Grant
		if g.GrantFn != nil {
			tok, err := g.GrantFn(repo)
			if err != nil {
				return err
			}
			auth = tok
		}
		if auth == "" {
			return fmt.Errorf("git: prepare grant required")
		}
	} else {
		auth = g.token()
	}
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
	if auth != "" {
		for _, e := range os.Environ() {
			if strings.HasPrefix(e, "GIT_SSL_NO_VERIFY=") || strings.HasPrefix(e, "GIT_SSL_CAINFO=") {
				continue
			}
			env = append(env, e)
		}
		env = append(env, "GIT_TERMINAL_PROMPT=0")
		fetch = []string{"-c", "http.extraHeader=Authorization: Bearer " + auth}
		if g.CAFile != "" {
			ca := g.CAFile
			if abs, err := filepath.Abs(ca); err == nil {
				ca = abs
			}
			env = append(env, "GIT_SSL_CAINFO="+ca)
			fetch = append(fetch, "-c", "http.sslCAInfo="+ca, "-c", "http.sslVerify=true")
		}
		fetch = append(fetch, "fetch", "--depth", "1", "origin", pin)
	}
	if err := gitDir(bin, dest, env, fetch...); err != nil {
		return err
	}
	if err := gitDir(bin, dest, nil, "checkout", "--force", "FETCH_HEAD"); err != nil {
		return err
	}
	public := "https://github.com/" + repo + ".git"
	return gitDir(bin, dest, nil, "remote", "set-url", "origin", public)
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

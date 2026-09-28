package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// syncMaxCommits bounds the landed-commit list printed by rusui sync.
const syncMaxCommits = 50

type syncSessionView struct {
	Cancelled   bool `json:"cancelled"`
	Publication *struct {
		Repo        string `json:"repo"`
		PullRequest int    `json:"pull_request"`
		SHA         string `json:"sha"`
	} `json:"publication"`
}

func syncCLI(args []string) {
	os.Exit(syncMain(args, os.Stdout, os.Stderr))
}

// syncMain fetches a session's published pull request head into a local
// checkout with the operator's own git remote and credentials. It reads
// the plane and never touches the environment or the working tree.
func syncMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_OPERATOR_TOKEN"), "operator token")
	remote := fs.String("remote", "origin", "git remote of the session's repository")
	dir := fs.String("dir", ".", "local git checkout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: rusui sync [-url URL] [-token TOKEN] [-remote NAME] [-dir DIR] SESSION_ID")
		return 2
	}
	id := fs.Arg(0)
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		_, _ = fmt.Fprintln(stderr, "sync: invalid session id")
		return 2
	}
	if err := runSync(*url, *token, *remote, *dir, id, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "sync: %v\n", err)
		return 1
	}
	return 0
}

func runSync(planeURL, token, remote, dir, id string, stdout io.Writer) error {
	res, err := planeClient(planeURL, token, "/sessions/"+id)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode == http.StatusNotFound {
		return fmt.Errorf("session %s not found", id)
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s %s", res.Status, bytesTrim(b))
	}
	var view syncSessionView
	if err := json.Unmarshal(b, &view); err != nil {
		return fmt.Errorf("response")
	}
	if view.Cancelled {
		return fmt.Errorf("session %s was cancelled", id)
	}
	pub := view.Publication
	if pub == nil {
		return fmt.Errorf("session %s has not published a pull request", id)
	}

	if _, err := git(dir, "rev-parse", "--git-dir"); err != nil {
		return fmt.Errorf("%s is not a git checkout", dir)
	}
	remoteURL, err := git(dir, "remote", "get-url", remote)
	if err != nil {
		return fmt.Errorf("no git remote %q", remote)
	}
	if !remoteNamesRepo(remoteURL, pub.Repo) {
		return fmt.Errorf("remote %q does not name %s; pass -remote", remote, pub.Repo)
	}

	ref := "refs/rusui/sessions/" + id
	old, _ := git(dir, "rev-parse", "-q", "--verify", ref+"^{commit}")
	if _, err := git(dir, "fetch", "--no-tags", remote, fmt.Sprintf("+refs/pull/%d/head:%s", pub.PullRequest, ref)); err != nil {
		return fmt.Errorf("fetch %s#%d: %w", pub.Repo, pub.PullRequest, err)
	}
	head, err := git(dir, "rev-parse", ref+"^{commit}")
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stdout, "session %s published %s#%d at %s\n", id, pub.Repo, pub.PullRequest, pub.SHA)
	if head != pub.SHA {
		_, _ = fmt.Fprintf(stdout, "note: pull request head is now %s\n", head)
	}
	switch old {
	case head:
		_, _ = fmt.Fprintf(stdout, "%s %s already up to date\n", ref, short(head))
		return nil
	case "":
		_, _ = fmt.Fprintf(stdout, "%s %s new\n", ref, short(head))
	default:
		if _, err := git(dir, "merge-base", "--is-ancestor", old, head); err != nil {
			_, _ = fmt.Fprintf(stdout, "%s %s...%s replaced\n", ref, short(old), short(head))
		} else {
			_, _ = fmt.Fprintf(stdout, "%s %s..%s\n", ref, short(old), short(head))
		}
	}
	// Landed commits: reachable from the fetched head, and neither from
	// the previous value of this ref nor from any other local ref.
	logArgs := []string{"log", "--format=%h %s", head, "--not"}
	if old != "" {
		logArgs = append(logArgs, old)
	}
	logArgs = append(logArgs, "--exclude="+ref, "--all")
	out, err := git(dir, logArgs...)
	if err != nil {
		return err
	}
	var commits []string
	if out != "" {
		commits = strings.Split(out, "\n")
	}
	noun := "commits"
	if len(commits) == 1 {
		noun = "commit"
	}
	_, _ = fmt.Fprintf(stdout, "%d %s landed\n", len(commits), noun)
	for i, c := range commits {
		if i == syncMaxCommits {
			_, _ = fmt.Fprintf(stdout, "... and %d more\n", len(commits)-syncMaxCommits)
			break
		}
		_, _ = fmt.Fprintf(stdout, "  %s\n", visibleText(c))
	}
	return nil
}

// git runs git in dir with the operator's own configuration and returns
// trimmed stdout. Rusui passes it no credential.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%s", msg)
		}
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// remoteNamesRepo reports whether a git remote URL ends in owner/repo,
// as HTTPS, SSH, scp-like, and file remotes do.
func remoteNamesRepo(remoteURL, repo string) bool {
	u := strings.TrimSuffix(strings.TrimRight(remoteURL, "/"), ".git")
	u = strings.ReplaceAll(u, ":", "/")
	return strings.HasSuffix(strings.ToLower(u), "/"+strings.ToLower(repo))
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

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
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sannrox/rusui/internal/env"
)

// syncMaxCommits bounds the landed-commit list printed by rusui sync.
const syncMaxCommits = 50

type syncSessionView struct {
	Cancelled bool `json:"cancelled"`
	Session   *struct {
		Repo     string `json:"repo"`
		Archived bool   `json:"archived"`
	} `json:"session"`
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
// checkout with the operator's own git remote and credentials. With
// -workspace it applies the environment HEAD and uncommitted files
// instead. It does not take the terminal write lease or start a turn.
func syncMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_OPERATOR_TOKEN"), "operator token")
	remote := fs.String("remote", "origin", "git remote of the session's repository")
	dir := fs.String("dir", ".", "local git checkout")
	workspace := fs.Bool("workspace", false, "apply the environment working tree instead of the published pull request")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := resolveOperatorToken(fs, *url, token); err != nil {
		_, _ = fmt.Fprintln(stderr, "sync:", err)
		return 1
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: rusui sync [-url URL] [-token TOKEN] [-remote NAME] [-dir DIR] [-workspace] SESSION_ID")
		return 2
	}
	id := fs.Arg(0)
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		_, _ = fmt.Fprintln(stderr, "sync: invalid session id")
		return 2
	}
	var err error
	if *workspace {
		err = runWorkspaceSync(*url, *token, *remote, *dir, id, stdout)
	} else {
		err = runSync(*url, *token, *remote, *dir, id, stdout)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sync: %v\n", err)
		return 1
	}
	return 0
}

func runSync(planeURL, token, remote, dir, id string, stdout io.Writer) error {
	view, err := loadSyncSession(planeURL, token, id)
	if err != nil {
		return err
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

func runWorkspaceSync(planeURL, token, remote, dir, id string, stdout io.Writer) error {
	view, err := loadSyncSession(planeURL, token, id)
	if err != nil {
		return err
	}
	if view.Session != nil && view.Session.Archived {
		return fmt.Errorf("session %s is archived", id)
	}
	if view.Cancelled {
		return fmt.Errorf("session %s was cancelled", id)
	}
	repo := ""
	if view.Session != nil {
		repo = view.Session.Repo
	}
	if repo == "" {
		return fmt.Errorf("session %s has no repository", id)
	}
	if _, err := git(dir, "rev-parse", "--git-dir"); err != nil {
		return fmt.Errorf("%s is not a git checkout", dir)
	}
	remoteURL, err := git(dir, "remote", "get-url", remote)
	if err != nil {
		return fmt.Errorf("no git remote %q", remote)
	}
	if !remoteNamesRepo(remoteURL, repo) {
		return fmt.Errorf("remote %q does not name %s; pass -remote", remote, repo)
	}

	res, err := planeClient(planeURL, token, "/sessions/"+id+"/workspace/export")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode == http.StatusNotFound {
		return fmt.Errorf("session %s not found", id)
	}
	if res.StatusCode >= 300 {
		msg := bytesTrim(body)
		switch {
		case strings.Contains(msg, "cancelled"):
			return fmt.Errorf("session %s was cancelled", id)
		case strings.Contains(msg, "archived"):
			return fmt.Errorf("session %s is archived", id)
		case strings.Contains(msg, "unpublished"):
			return fmt.Errorf("session %s has no git workspace", id)
		}
		return fmt.Errorf("%s %s", res.Status, msg)
	}
	head, bundle, files, err := env.ReadWorkspaceExport(bytes.NewReader(body))
	if err != nil {
		return err
	}

	if workspaceMatches(dir, head, files) {
		_, _ = fmt.Fprintf(stdout, "session %s workspace %s already up to date\n", id, head)
		return nil
	}
	if dirty, err := git(dir, "status", "--porcelain"); err != nil {
		return err
	} else if dirty != "" {
		return fmt.Errorf("local checkout is dirty")
	}

	tmp, err := os.MkdirTemp("", fmt.Sprintf("rusui-sync-%d-*", os.Getpid()))
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	bundlePath := filepath.Join(tmp, "bundle")
	if err := os.WriteFile(bundlePath, bundle, 0o600); err != nil {
		return err
	}
	if _, err := git(dir, "fetch", "--no-tags", bundlePath, "+HEAD:refs/rusui/workspace/"+id); err != nil {
		return fmt.Errorf("fetch workspace: %w", err)
	}
	if _, err := git(dir, "checkout", "-q", "--detach", head); err != nil {
		return err
	}
	if err := applyExportTree(dir, files); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "session %s workspace %s\n", id, head)
	return nil
}

func loadSyncSession(planeURL, token, id string) (syncSessionView, error) {
	var view syncSessionView
	res, err := planeClient(planeURL, token, "/sessions/"+id)
	if err != nil {
		return view, err
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode == http.StatusNotFound {
		return view, fmt.Errorf("session %s not found", id)
	}
	if res.StatusCode >= 300 {
		return view, fmt.Errorf("%s %s", res.Status, bytesTrim(b))
	}
	if err := json.Unmarshal(b, &view); err != nil {
		return view, fmt.Errorf("response")
	}
	return view, nil
}

func workspaceMatches(dir, head string, files map[string]env.ExportEntry) bool {
	got, err := git(dir, "rev-parse", "HEAD")
	if err != nil || got != head {
		return false
	}
	for rel, ent := range files {
		if !exportEntryMatches(dir, rel, ent) {
			return false
		}
	}
	tracked, err := git(dir, "ls-tree", "-r", "--name-only", "HEAD")
	if err != nil {
		return false
	}
	if tracked != "" {
		for rel := range strings.SplitSeq(tracked, "\n") {
			if _, ok := files[rel]; ok {
				continue
			}
			if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
				return false
			}
		}
	}
	status, err := git(dir, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil || status == "" {
		return err == nil
	}
	for line := range strings.SplitSeq(status, "\n") {
		rel, ok := porcelainPath(line)
		if !ok {
			continue
		}
		if _, inExport := files[rel]; inExport {
			continue
		}
		if len(line) >= 2 && (line[0] == 'D' || line[1] == 'D') {
			continue
		}
		return false
	}
	return true
}

func porcelainPath(line string) (string, bool) {
	if len(line) < 4 {
		return "", false
	}
	path := line[3:]
	if i := strings.Index(path, " -> "); i >= 0 {
		path = path[i+4:]
	}
	path = strings.TrimSpace(path)
	return path, path != ""
}

func exportEntryMatches(dir, rel string, ent env.ExportEntry) bool {
	path := filepath.Join(dir, filepath.FromSlash(rel))
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	if ent.Link != "" {
		target, err := os.Readlink(path)
		return err == nil && target == ent.Link
	}
	if !info.Mode().IsRegular() {
		return false
	}
	body, err := os.ReadFile(path)
	return err == nil && bytes.Equal(body, ent.Body)
}

func applyExportTree(dir string, files map[string]env.ExportEntry) error {
	tracked, err := git(dir, "ls-tree", "-r", "--name-only", "HEAD")
	if err != nil {
		return err
	}
	if tracked != "" {
		for rel := range strings.SplitSeq(tracked, "\n") {
			if _, ok := files[rel]; ok {
				continue
			}
			_ = os.Remove(filepath.Join(dir, filepath.FromSlash(rel)))
		}
	}
	for rel, ent := range files {
		if !filepath.IsLocal(filepath.FromSlash(rel)) || strings.Contains("/"+rel+"/", "/.git/") {
			return fmt.Errorf("export path")
		}
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		_ = os.Remove(path)
		if ent.Link != "" {
			if err := os.Symlink(ent.Link, path); err != nil {
				return err
			}
			continue
		}
		mode := ent.Mode
		if mode == 0 {
			mode = 0o600
		}
		if err := os.WriteFile(path, ent.Body, mode); err != nil {
			return err
		}
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

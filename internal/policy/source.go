package policy

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	ProviderGitHub     = "github"
	ProviderGitLab     = "gitlab"
	GitLabOrigin       = "https://gitlab.com"
	gitLabSourcePrefix = "gitlab.com:"
)

// GitLabSourceKey is the store identity for a GitLab.com project. It cannot
// alias a GitHub owner/name, which never contains ':'.
func GitLabSourceKey(projectID int64) string {
	return gitLabSourcePrefix + strconv.FormatInt(projectID, 10)
}

// IsGitLabSource reports whether repo is a GitLab.com bound source.
func IsGitLabSource(repo string) bool {
	_, ok := ParseGitLabSource(repo)
	return ok
}

// ParseGitLabSource returns the GitLab.com project id encoded in repo.
func ParseGitLabSource(repo string) (projectID int64, ok bool) {
	rest, found := strings.CutPrefix(repo, gitLabSourcePrefix)
	if !found || rest == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func validGitHubRepoName(name string) error {
	if name == "" || strings.Contains(name, ":") || strings.ContainsAny(name, " \t\n") {
		return fmt.Errorf("policy: invalid repository %q", name)
	}
	owner, repo, ok := strings.Cut(name, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return fmt.Errorf("policy: invalid repository %q", name)
	}
	return nil
}

func validGitLabPath(path string) error {
	if path == "" || strings.Contains(path, ":") || strings.ContainsAny(path, " \t\n") {
		return fmt.Errorf("policy: invalid gitlab path %q", path)
	}
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return fmt.Errorf("policy: invalid gitlab path %q", path)
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return fmt.Errorf("policy: invalid gitlab path %q", path)
		}
	}
	return nil
}

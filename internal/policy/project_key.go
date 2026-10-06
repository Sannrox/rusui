package policy

import "strings"

const projectKeyPrefix = "project:"

// ProjectKey is the reserved sessions.repo value for a project with no
// bound repository (ADR 0048). A policy repository is owner/name and never
// contains ':'.
func ProjectKey(slug string) string {
	return projectKeyPrefix + slug
}

// ParseProjectKey reports the project slug when repo is a project key.
func ParseProjectKey(repo string) (string, bool) {
	if !strings.HasPrefix(repo, projectKeyPrefix) {
		return "", false
	}
	slug := repo[len(projectKeyPrefix):]
	if err := validSlug(slug); err != nil {
		return "", false
	}
	return slug, true
}

// IsProjectKey reports whether repo is a reserved project key.
func IsProjectKey(repo string) bool {
	_, ok := ParseProjectKey(repo)
	return ok
}

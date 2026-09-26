package policy

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type RepoExplanation struct {
	Repository string
	Repo       Repo
	Project    Project
	Sources    map[string]string
}

// ExplainRepo returns the effective repository and project values with their
// source in the policy document.
func (e *Effective) ExplainRepo(name string) (RepoExplanation, bool, error) {
	r, ok := e.Repo(name)
	if !ok {
		return RepoExplanation{Repository: name}, false, nil
	}
	p, ok := e.Project(r.Project)
	if !ok {
		return RepoExplanation{}, false, fmt.Errorf("policy: project %q is missing", r.Project)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(e.Raw, &root); err != nil {
		return RepoExplanation{}, false, fmt.Errorf("policy: read source values: %w", err)
	}
	rootMap := &root
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		rootMap = root.Content[0]
	}
	defaults := mappingValue(rootMap, "defaults")
	project := mappingValue(mappingValue(rootMap, "projects"), r.Project)
	repository := mappingValue(mappingValue(project, "repos"), name)
	if project == nil || repository == nil {
		return RepoExplanation{}, false, fmt.Errorf("policy: source values for %q are missing", name)
	}
	sources := map[string]string{}
	for _, field := range []string{"visibility", "review", "comments", "close", "implement", "land", "max_reviews_per_repo_per_utc_day"} {
		sources[field] = repoSource(field, repository, defaults)
	}
	sources["never_release"] = "enforced by v2 parser"
	sources["never_leak_private_to_public"] = "enforced by v2 parser"
	for _, field := range []string{"session_kinds", "egress"} {
		sources[field] = projectSource(field, project, defaults)
	}
	budgets := mappingValue(project, "budgets")
	if mappingValue(budgets, "max_concurrent_leases") != nil {
		sources["budgets.max_concurrent_leases"] = "project"
	} else {
		sources["budgets.max_concurrent_leases"] = "schema default"
	}
	if effectiveSetting(mappingValue(project, "permissions"), "permissions", false) {
		sources["permissions"] = "project"
	} else {
		sources["permissions"] = "schema default"
	}
	if effectiveSetting(mappingValue(project, "local_runtime"), "local_runtime", false) {
		sources["local_runtime"] = "project"
	} else {
		sources["local_runtime"] = "schema default"
	}
	return RepoExplanation{Repository: name, Repo: r, Project: p, Sources: sources}, true, nil
}

func repoSource(field string, repo, defaults *yaml.Node) string {
	if effectiveSetting(mappingValue(repo, field), field, true) {
		return "repository override"
	}
	if effectiveSetting(mappingValue(defaults, field), field, false) {
		return "defaults"
	}
	return "schema default"
}

func projectSource(field string, project, defaults *yaml.Node) string {
	if effectiveSetting(mappingValue(project, field), field, false) {
		return "project"
	}
	if effectiveSetting(mappingValue(defaults, field), field, false) {
		return "defaults"
	}
	return "schema default"
}

func effectiveSetting(node *yaml.Node, field string, repoOverride bool) bool {
	node = dereferenceAlias(node)
	if node == nil || node.Tag == "!!null" {
		return false
	}
	switch field {
	case "session_kinds":
		return node.Kind == yaml.SequenceNode && len(node.Content) > 0
	case "egress", "visibility":
		return node.Value != ""
	case "max_reviews_per_repo_per_utc_day":
		return repoOverride || node.Value != "0"
	default:
		return true
	}
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	return mappingValueSeen(node, key, map[*yaml.Node]bool{})
}

func mappingValueSeen(node *yaml.Node, key string, seen map[*yaml.Node]bool) *yaml.Node {
	node = dereferenceAlias(node)
	if node != nil && node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		node = node.Content[0]
	}
	if node == nil || node.Kind != yaml.MappingNode || seen[node] {
		return nil
	}
	seen[node] = true
	defer delete(seen, node)
	var merged *yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		if isMergeKey(node.Content[i]) {
			merged = node.Content[i+1]
			continue
		}
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return mergedValue(merged, key, seen)
}

func mergedValue(node *yaml.Node, key string, seen map[*yaml.Node]bool) *yaml.Node {
	node = dereferenceAlias(node)
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.MappingNode:
		return mappingValueSeen(node, key, seen)
	case yaml.SequenceNode:
		for _, merged := range node.Content {
			if value := mappingValueSeen(merged, key, seen); value != nil {
				return value
			}
		}
	}
	return nil
}

func dereferenceAlias(node *yaml.Node) *yaml.Node {
	seen := map[*yaml.Node]bool{}
	for node != nil && node.Kind == yaml.AliasNode {
		if seen[node] || node.Alias == nil {
			return nil
		}
		seen[node] = true
		node = node.Alias
	}
	return node
}

func isMergeKey(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.ScalarNode && node.Value == "<<" &&
		(node.Tag == "" || node.Tag == "!" || node.Tag == "!!merge")
}

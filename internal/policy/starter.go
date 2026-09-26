package policy

const starterYAML = `# Operator default (policy v2, keyed by project). Copy to policy.yaml.
# comments/close authorize v1 dry-run intended-actions, not live GitHub writes.
# Unknown fields fail closed.
version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
  review: true
  comments: false
  close: false
  implement: false
  land: false
  max_reviews_per_repo_per_utc_day: 50
projects:
  rusui:
    repos:
      Sannrox/rusui:
        visibility: public
        review: true
        comments: false
        close: false
        implement: false
        land: false
`

// StarterYAML returns a copy of the conservative operator policy template.
func StarterYAML() []byte {
	return []byte(starterYAML)
}

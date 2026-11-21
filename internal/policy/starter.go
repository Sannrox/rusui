package policy

const starterYAML = `# Operator default (policy v2, keyed by project). Copy to policy.yaml.
# comments/close authorize v1 dry-run intended-actions, not live GitHub writes.
# comment authorizes live GitHub comments (default off).
# advisory_digest groups routine inbox completions over 10 actionable
# items per UTC day (ADR 0038 D6). Default off. Overlay cannot enable it.
# repair is a category name or empty (off). Overlay cannot enable it.
# share is a project opt-in for operator-invited viewer preview grants
# (ADR 0096). Default off. Overlay cannot enable it. TTL is 1h, 4h, or 24h.
# package_registry names one already-granted package-registry host for a
# plane-minted turn identity (ADR 0097). Empty is off. Overlay cannot
# enable it and may only remove it. Unknown fields fail closed.
# session_export_sources lists source plane ids admitted for rusui
# import-session (ADR 0100). Empty/omit is off. Overlay may only remove
# (session_export_source:<plane_id>=0).
version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
  review: true
  comments: false
  comment: false
  advisory_digest: false
  close: false
  implement: false
  land: false
  repair: ""
  protected_labels: [rusui:hold, security]
  max_reviews_per_repo_per_utc_day: 50
  max_repairs_per_repo_per_utc_day: 1
# The four existing protocols. Empty image uses RUSUI_GUEST_IMAGE.
# Omitting this registry preserves the built-in entries.
guests:
  grok:
    protocol: acp
    argv:
    - agent
    - --permission-mode
    - default
    - agent
    - stdio
    probe:
    - agent
    - --version
    pin: acp:1
    image: ''
  claude:
    protocol: claude-stream-json
    argv:
    - claude
    - --print
    - --input-format
    - stream-json
    - --output-format
    - stream-json
    - --verbose
    - --permission-mode
    - default
    - --permission-prompt-tool
    - stdio
    - --settings
    - '{"permissions":{"ask":["Bash"]}}'
    probe:
    - claude
    - --version
    pin: 2.1.283
    image: ''
  codex:
    protocol: codex-app-server
    argv:
    - codex
    - app-server
    - --listen
    - stdio://
    probe:
    - codex
    - --version
    pin: app-server-2026-04-15
    image: ''
  shikigami:
    protocol: acp
    argv:
    - shikigami
    - --state
    - ./state
    - acp
    probe:
    - shikigami
    - --version
    pin: acp
    image: ''
projects:
  rusui:
    size: medium
    share: false
    # package_registry: registry.example.com
    # Optional guest choice; omit to retain RUSUI_GUEST fallback.
    # guests:
    #   default: shikigami
    #   allowed: [grok, claude, codex, shikigami]
    repos:
      Sannrox/rusui:
        visibility: public
        review: true
        comments: false
        comment: false
        advisory_digest: false
        close: false
        implement: false
        land: false
        repair: ""
    # GitLab.com project issues only (ADR 0021). Identity is project_id,
    # not the path. Overlay cannot add this map. Write flags are refused.
    # gitlab:
    #   group/project:
    #     project_id: 123
    #     visibility: public
    #     review: true
`

// StarterYAML returns a copy of the conservative operator policy template.
func StarterYAML() []byte {
	return []byte(starterYAML)
}

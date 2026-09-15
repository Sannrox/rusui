# ADR 0005: Policy v2 is keyed by project

- Status: Accepted
- Date: 2026-09-15
- Amends: [ADR 0001](0001-environment-plane.md) D9 (local policy v2
  identity). Overlay-may-only-narrow, fail-closed unknown fields, and
  `governance.required` as P3 stand.
- Resolves: [#41](https://github.com/Sannrox/rusui/issues/41)
- Unblocks: [#45](https://github.com/Sannrox/rusui/issues/45)
- Discussion: none. GitHub Discussions are disabled; the pull request
  that adds this file is the review venue. Merging with status Accepted
  is the acceptance act.

## Context

v1 `policy.yaml` is keyed by GitHub `owner/name`. Overlay pause is
`pause:global` or `pause:<repo>`. ACP `session/request_permission` has
no YAML rules (`DenyUnmatched`): every request is unmatched.

P1 dogfood needs budgets, egress class, session-kind allowlists, and
`rusui run` on a tree that may have no GitHub remote. Keeping the
GitHub repository as the policy identity makes those hang off the
wrong noun. [Find the way to P1 dogfood](https://github.com/Sannrox/rusui/issues/37)
locked policy v2 as keyed by **project**; this ADR defines that noun.

## Decision

A **project** is an operator-named policy and budget domain.

- A session belongs to exactly one project. An environment belongs to
  exactly one project. A GitHub repository binds to at most one
  project.
- GitHub is optional. The **review** session kind requires at least
  one bound repository. A project with no repos may still run `run`
  and `scheduled`.
- The YAML key is a slug (`rusui`), not `Sannrox/rusui`. Bound
  repositories stay `owner/name`.
- GitHub flags (`review`, `comments`, `close`, `implement`, `land`,
  `visibility`, `max_reviews_per_repo_per_utc_day`) live **per bound
  repository**. `implement` and `land` remain in the file and stay
  false under the current contract.
- Executable baseline families on `projects.<slug>`: bound repos and
  their GitHub flags, `session_kinds` allowlist (`review` | `run` |
  `scheduled`), `egress` class, budget **limits** per UTC day,
  permission **allow-rules**. Unknown fields fail closed. Overlay
  cannot add a capability.
- `session/request_permission`: an allow-rule matching tool / kind /
  command class is granted and receipted. Unmatched is denied and
  parked on the approvals inbox. Overlay cannot add an allow-rule.
- Overlay pause keys are `pause:global` and `pause:<project>`. Slack
  `pause rusui` names the project slug. No per-repo pause.
- Budget *sources* (how minutes, tokens, or dollars are collected),
  `governance.required`, snapshot/setup rules, and runner placement
  are not in this document.

Dogfood instance (not the definition): two projects `rusui` and
`shikigami`, each binding one repository.

The shipped parser still reads version 1 until a delivery issue ports
it. This ADR is the contract that issue implements.

## Consequences

- Admit, claim, pause, and ACP permission lookup key on project slug.
- A GitHub event for a repo bound to no project in the current
  revision is out of scope, same as a missing v1 repo key.
- Removing a project from YAML cannot be undone by overlay.
- [How do review, run, and schedule sessions start from the object API?](https://github.com/Sannrox/rusui/issues/45)
  can treat `project → environment → session → turn` as the object
  graph.

## Rejected alternatives

- **1:1 with a GitHub repository.** Cheapest migration; `rusui run`
  without GitHub has nowhere to hang.
- **Project is a source tree.** Collides with environment snapshot
  identity.
- **Project is an environment class** (image, size, driver). Driver
  config, not policy.
- **Keep per-repo pause.** A repo has one project; a second pause
  key duplicates it.
- **No YAML permission rules in P1.** Every shell is an approval;
  unattended dogfood will not hold.
- **Drop GitHub flags and keep only session-kind allowlists.** Apply
  would have no executable comment/close flags.

## Validation and reversal

Accept on merge. Reopen if a session must belong to two projects, or
a GitHub repository must bind to two projects.

## Sources

- [#41](https://github.com/Sannrox/rusui/issues/41)
- [Find the way to P1 dogfood](https://github.com/Sannrox/rusui/issues/37)
- [ADR 0001](0001-environment-plane.md) D9
- `policy.yaml` / `internal/policy` version 1 on `origin/main`
- `internal/acp.DenyUnmatched`

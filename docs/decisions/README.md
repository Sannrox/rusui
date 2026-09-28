# Architecture decision records

Decisions that must outlive a single pull request.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](0001-environment-plane.md) | rusui becomes the environment plane for coding agents | Accepted |
| [0002](0002-grok-acp-agent-set.md) | P1 agent set is Grok CLI over ACP | Accepted |
| [0003](0003-operator-surface.md) | Operator surfaces are views of one object API | Accepted |
| [0004](0004-rusui-services-yaml.md) | services.yaml is `.rusui/services.yaml` only | Accepted |
| [0005](0005-policy-v2-project.md) | Policy v2 is keyed by project | Accepted |
| [0006](0006-session-start.md) | How review, run, and schedule sessions start | Accepted |
| [0007](0007-environment-snapshot.md) | P1 environment snapshot identity | Accepted |
| [0008](0008-p1-isolation-split.md) | P1 isolation split | Accepted |
| [0009](0009-credential-broker.md) | P1 credential broker | Accepted |
| [0010](0010-hybrid-roadmap-sequence.md) | Hybrid core-1.0 sequence and earlier gate mapping | Accepted |
| [0011](0011-unattended-session-contract.md) | Recoverable unattended-session contract | Accepted |
| [0012](0012-operator-access.md) | Single-operator access for terminal and preview | Accepted |
| [0013](0013-publication-authority.md) | Exact-artifact verification and plane-owned publication | Superseded by 0015 |
| [0014](0014-pilot-evaluation-deferred.md) | Ten-task pilot evaluation is deferred pending live plane-owned publication | Superseded by 0023 |
| [0015](0015-agent-publication.md) | The agent publishes its own pull requests | Accepted |
| [0016](0016-local-interactive-runtime.md) | Local interactive runtime boundary with Sumika | Accepted |
| [0017](0017-claude-guest-and-model-upstream.md) | Claude Code guest, operator model upstream, and a fenced self-test | Accepted |
| [0018](0018-rusui-setup.md) | `rusui setup` provisions a host; `rusui diagnose` verifies it | Accepted |
| [0019](0019-rusui-attach-client.md) | One Rusui attach command selects the runtime-owned transport | Accepted |
| [0020](0020-turn-scoped-github-publication.md) | Turn-scoped GitHub publication stays behind the plane | Accepted |
| [0021](0021-gitlab-intake.md) | First GitLab intake is GitLab.com project issues only | Accepted |
| [0022](0022-public-repo-isolation.md) | Public-repository unattended sessions default to the container driver | Accepted |
| [0023](0023-p8-pilot-narrow.md) | P8 ten-task live pilot is narrow | Accepted |
| [0024](0024-session-surface.md) | The CLI and the console open a session | Accepted |
| [0025](0025-provider-boundary.md) | One Go provider boundary for Grok, Claude, and Codex | Accepted |
| [0026](0026-harness-model-upstream.md) | Harness, model, and upstream are independent | Accepted |
| [0027](0027-guest-reachability-ask.md) | The guest image does not declare reachability yet | Accepted |
| [0028](0028-container-isolation-profile.md) | The supported machine isolation profile remains the container | Accepted |
| [0029](0029-single-host-runner.md) | The supported runner topology remains one host | Accepted |
| [0030](0030-schedule-new-session.md) | A schedule fire starts a new session | Accepted |
| [0031](0031-cli-command-tree.md) | The rusui CLI stays a flat verb list | Accepted |
| [0032](0032-named-services.md) | Named services declare a port and a health path | Accepted |
| [0033](0033-third-party-identity-deferred.md) | Plane-minted third-party identity is deferred | Accepted |
| [0034](0034-operator-host-location-deferred.md) | A registered operator host is not a session location yet | Accepted |
| [0035](0035-live-environment-fork-deferred.md) | Live environment fork is deferred | Accepted |

## When to write an ADR

Add an ADR when a choice:

- changes a durable boundary (policy schema, worker or runner contract,
  persistence strategy, trust model, public API);
- chooses among durable alternatives;
- would be expensive to reverse without a migration story.

Small bug fixes and local refactors do not need ADRs. Capture them in code,
tests, and the pull request description instead.

## Lifecycle

- **Proposed**: investigation recorded (open or merged pull request).
  `ARCHITECTURE.md` remains the product contract until acceptance.
- **Accepted**: merged with status updated; follow-up Issues may reference the
  ADR as a delivered dependency.
- **Superseded**: a newer ADR links here and this file links forward. Historical
  text is retained.

Template: [template.md](template.md). Title, status block, context, decision,
consequences, rejected alternatives, validation and reversal, sources. See
0001 for style. An accepted ADR does not change
[ARCHITECTURE.md](../../ARCHITECTURE.md) until that file is rewritten.

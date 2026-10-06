# Architecture decision records

Decisions that must outlive a single pull request.

> **Historical decision record.** Read each status before applying a decision.
> Superseded records preserve the earlier reasoning; they do not define the
> current contract. [ARCHITECTURE.md](../../ARCHITECTURE.md) remains the
> product contract. Proposed direction remains proposed until accepted.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](0001-environment-plane.md) | rusui becomes the environment plane for coding agents | Accepted |
| [0002](0002-grok-acp-agent-set.md) | P1 agent set is Grok CLI over ACP | Accepted; amended by 0025, 0060 |
| [0003](0003-operator-surface.md) | Operator surfaces are views of one object API | Accepted |
| [0004](0004-rusui-services-yaml.md) | services.yaml is `.rusui/services.yaml` only | Accepted |
| [0005](0005-policy-v2-project.md) | Policy v2 is keyed by project | Accepted |
| [0006](0006-session-start.md) | How review, run, and schedule sessions start | Accepted |
| [0007](0007-environment-snapshot.md) | P1 environment snapshot identity | Accepted; amended by 0048, 0049, 0054 |
| [0008](0008-p1-isolation-split.md) | P1 isolation split | Accepted |
| [0009](0009-credential-broker.md) | P1 credential broker | Accepted; amended by 0053 |
| [0010](0010-hybrid-roadmap-sequence.md) | Hybrid core-1.0 sequence and earlier gate mapping | Accepted; amended by 0046 |
| [0011](0011-unattended-session-contract.md) | Recoverable unattended-session contract | Accepted |
| [0012](0012-operator-access.md) | Single-operator access for terminal and preview | Accepted; amended by 0032, 0036, 0039, 0059 |
| [0013](0013-publication-authority.md) | Exact-artifact verification and plane-owned publication | Superseded by 0015 |
| [0014](0014-pilot-evaluation-deferred.md) | Ten-task pilot evaluation is deferred pending live plane-owned publication | Superseded by 0023 |
| [0015](0015-agent-publication.md) | The agent publishes its own pull requests | Accepted; amended by 0055 |
| [0016](0016-local-interactive-runtime.md) | Local interactive runtime boundary with Sumika | Accepted |
| [0017](0017-claude-guest-and-model-upstream.md) | Claude Code guest, operator model upstream, and a fenced self-test | Accepted |
| [0018](0018-rusui-setup.md) | `rusui setup` provisions a host; `rusui diagnose` verifies it | Accepted |
| [0019](0019-rusui-attach-client.md) | One Rusui attach command selects the runtime-owned transport | Accepted; amended by 0052 |
| [0020](0020-turn-scoped-github-publication.md) | Turn-scoped GitHub publication stays behind the plane | Accepted |
| [0021](0021-gitlab-intake.md) | First GitLab intake is GitLab.com project issues only | Accepted |
| [0022](0022-public-repo-isolation.md) | Public-repository unattended sessions default to the container driver | Accepted |
| [0023](0023-p8-pilot-narrow.md) | P8 ten-task live pilot is narrow | Accepted |
| [0024](0024-session-surface.md) | The CLI and the console open a session | Accepted |
| [0025](0025-provider-boundary.md) | One Go provider boundary for Grok, Claude, and Codex | Accepted |
| [0026](0026-harness-model-upstream.md) | Harness, model, and upstream are independent | Accepted; amended by 0061 |
| [0027](0027-guest-reachability-ask.md) | The guest image does not declare reachability yet | Accepted |
| [0028](0028-container-isolation-profile.md) | The supported machine isolation profile remains the container | Accepted |
| [0029](0029-single-host-runner.md) | The supported runner topology remains one host | Accepted |
| [0030](0030-schedule-new-session.md) | A schedule fire starts a new session | Accepted; amended by 0056 |
| [0031](0031-cli-command-tree.md) | The rusui CLI stays a flat verb list | Accepted |
| [0032](0032-named-services.md) | Named services declare a port and a health path | Accepted |
| [0033](0033-third-party-identity-deferred.md) | Plane-minted third-party identity is deferred | Accepted |
| [0034](0034-operator-host-location-deferred.md) | A registered operator host is not a session location yet | Accepted |
| [0035](0035-live-environment-fork-deferred.md) | Live environment fork is deferred | Accepted |
| [0036](0036-environment-desktop-deferred.md) | A graphical desktop in the environment is deferred | Accepted |
| [0037](0037-shared-preview-deferred.md) | Sharing a preview without operator authentication is deferred | Accepted |
| [0038](0038-maintenance-eligibility-and-promotion.md) | Maintenance eligibility and separate action-promotion gates | Accepted |
| [0039](0039-shared-operator-governance-deferred.md) | Shared-operator governance is deferred | Accepted; amended by 0059 |
| [0040](0040-child-session-delegation-deferred.md) | Child-session delegation is deferred | Accepted; amended by 0058 |
| [0041](0041-ha-plane-deferred.md) | An HA plane is deferred | Accepted |
| [0042](0042-disconnected-execution-deferred.md) | Disconnected execution is deferred | Accepted |
| [0043](0043-extension-contract-deferred.md) | An extension contract is deferred | Accepted |
| [0044](0044-plane-publishes-from-turn-result.md) | The plane publishes from the turn result | Accepted |
| [0045](0045-local-unattended-promotion-deferred.md) | Local unattended promotion is deferred | Accepted |
| [0046](0046-workspace-first-sequence.md) | Terminal-first remote workspace sequence | Accepted |
| [0047](0047-guest-link.md) | Container guests reach the plane only through a stdio guest link | Accepted; amended by 0050 |
| [0048](0048-pinless-run-session.md) | A project without a repository runs pinless sessions claimed by project key | Accepted |
| [0049](0049-idle-environment-expiry-retained.md) | Idle environment expiry remains 72 hours | Accepted |
| [0050](0050-guest-toolchain-in-image.md) | Bake guest toolchain into the image; add no destinations | Accepted |
| [0051](0051-fail-on-full-lease-budget-retained.md) | Admission still fails when the lease budget is full | Accepted |
| [0052](0052-separate-attach-and-guest-shells.md) | Attach and the guest keep separate shells | Accepted |
| [0053](0053-wake-secret-injection-refused.md) | The guest still holds only the per-turn grant | Accepted |
| [0054](0054-repository-hooks-only.md) | Setup and resume stay repository hooks | Accepted |
| [0055](0055-implement-publication-is-pull-request.md) | Implement publication stays a pull request | Accepted |
| [0056](0056-schedule-still-mints-a-new-session.md) | A schedule fire still mints a new session | Accepted |
| [0057](0057-github-intake-stays-review.md) | GitHub intake stays issues, pull requests, and comments | Accepted |
| [0058](0058-child-session-delegation-retained.md) | Child-session delegation stays deferred | Accepted |
| [0059](0059-unattributed-operator-retained.md) | Plane calls stay one unattributed operator | Accepted |
| [0060](0060-shikigami-acp-guest-pin.md) | Pin shikigami acp as a supported guest | Accepted |
| [0061](0061-one-model-upstream-retained.md) | One model upstream retained | Accepted |

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

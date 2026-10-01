# Roadmap

> **Not the runbook.** Sequencing estimates, not deadlines or the product
> contract ([ARCHITECTURE.md](ARCHITECTURE.md)).
>
> [ADR 0046](docs/decisions/0046-workspace-first-sequence.md) is **Accepted**
> and amends the core sequence in [ADR 0010](docs/decisions/0010-hybrid-roadmap-sequence.md).
> This file is the published workspace-first sequence. The earlier P0–P4 calendar
> remains at
> [94aabd7/ROADMAP.md](https://github.com/Sannrox/rusui/blob/94aabd7d292faa052ec8faba0f028f8e07bc9ade/ROADMAP.md).
> [ARCHITECTURE.md](ARCHITECTURE.md) names agent-owned publication from
> implement sessions ([ADR 0015](docs/decisions/0015-agent-publication.md),
> superseding ADR 0013).

GitHub Issues remain the planning source of truth; this file links to them
and never overrides their `## Dependencies` sections. Milestone IDs stay
stable for existing Issue links; the core order is M1 → M2 → M4 → M6.
M3 maintenance and M5 infrastructure or agent-choice tracks are optional.
Re-estimate after each live evidence gate. Reduce scope before moving a
safety or correctness gate.

ADR 0015 lets the agent in an implement session push and open its own pull
request with the operator's credential. Human merge remains required.
No live comment, close, merge, or land action is enabled by this sequence.

[#141](https://github.com/Sannrox/rusui/issues/141) still lists
[#110](https://github.com/Sannrox/rusui/issues/110) as a dependency. Its
Issue body requires a separate authorized update before the new core path
can be treated as ready. A changed roadmap does not silently edit Issue
dependencies.

## Shape

| Horizon | Operator outcome | Place in sequence |
| --- | --- | --- |
| **M1 Dependable sessions** | Leave a real managed session, return, steer, recover interruptions | Core gate |
| **M2 Verified delivery** | One bounded issue becomes an attributable PR; humans merge | Core gate |
| **M4 Daily remote workspace** | Complete the session and delivery loop from the terminal; independent install/restore | Core gate |
| **M6 Stable core** | Freeze the demonstrated terminal workflow and support limits | Core 1.0 gate |
| **M3 Continuous maintenance** | Two repositories stay reviewed; eligible repairs run under policy | Separate maintenance release gate |
| **M5 Agent and infrastructure choice** | Add selected capabilities when measured need justifies them | Optional capability releases |

M1 → M2 → M4 → M6 is the core path. The optional tracks can proceed
independently after their own dependencies and adoption decisions. A closed
component Issue or a narrower proof is not a passing release gate.

## M1 Dependable sessions

Exit for the core path: a predeclared real-container/guest terminal workflow
including follow-ups and deliberate faults. The earlier twenty-run
[D6 proof](docs/proofs/d6-session-workflow.md) covered a bounded lifecycle
but did not run a live guest in that matrix. The
[unified live workflow](docs/proofs/186-unified-workflow-results.md) recorded
identity failures and a rerun gate. Process driver is not production
isolation. [ADR 0009](docs/decisions/0009-credential-broker.md) and
[ADR 0011](docs/decisions/0011-unattended-session-contract.md) stand.

Planning issues: [#88](https://github.com/Sannrox/rusui/issues/88)–[#95](https://github.com/Sannrox/rusui/issues/95).
Closed component issues are not this exit gate.

## M2 Verified delivery

May start after M1's bounded session proof **and** an accepted publication
contract. It does **not** wait for thirty unattended days. Humans merge.
[ADR 0015](docs/decisions/0015-agent-publication.md) is **Accepted** and
supersedes ADR 0013: the agent in an implement session publishes its own
pull request as the operator, with `Co-authored-by: rusui` and
`Rusui-Session` trailers and no plane proof gate. Comment, close, merge,
and land stay unauthorized; token scope and branch protection enforce
that.

Planning issues: [#96](https://github.com/Sannrox/rusui/issues/96)–[#103](https://github.com/Sannrox/rusui/issues/103).
The [P8 live pilot](docs/proofs/p8-pilot-results.md) ended narrow. A new
predeclared cohort must demonstrate reliable bounded publication and
follow-up updates before a broad delivery claim.

## M3 Continuous maintenance (separate release gate)

Retains the original P1 observation: thirty measured days on rusui plus
one sibling repository at twenty or more sessions per week with zero
observed credential leaks and no unauthorized action in the measured run.
A finite sample is evidence, not a future guarantee. This gate is required
for a maintenance release or autonomous-maintenance claim, not for the
remote-workspace 1.0. Live comment/close/merge are separate promotions.

Planning issues: [#104](https://github.com/Sannrox/rusui/issues/104)–[#110](https://github.com/Sannrox/rusui/issues/110).

## M4 Daily terminal workspace

CLI and HTTP+SSE are the primary path. An operator must be able to start,
follow, inspect, steer, approve, attach to, and recover a managed session,
then obtain its result, without using the browser console. Terminal access
to services and previews belongs here when needed to verify the task.
The existing console and editor ACP facade remain supported surfaces;
their completeness is not a core-1.0 gate under
[ADR 0046](docs/decisions/0046-workspace-first-sequence.md).

The [U8 self-hosting journey](docs/proofs/u8-operator-journey.md) was an
in-process author exercise. Core exit needs a documented terminal-only
exercise with shipped binaries and an independent self-hosting maintainer,
including installation, disconnect/reconnect, upgrade, and restore.

Planning issues: [#111](https://github.com/Sannrox/rusui/issues/111)–[#118](https://github.com/Sannrox/rusui/issues/118).

## M5 Optional tracks (not required for core 1.0)

Each earlier gate stays named. Absence from core 1.0 is allowed only as
recorded in [ADR 0046](docs/decisions/0046-workspace-first-sequence.md).

| Track | Treatment |
| --- | --- |
| MicroVM / snapshot wake under 2 s | Optional. Container stop/start is sufficient for M1 |
| Child-session delegation and live fork | Optional |
| Runner fleet | Optional |
| Postgres and HA plane | Optional. SQLite-first until a measured RPO/RTO requires otherwise |
| Shared sessions / extra governance | Optional, only if multiple operators exist |
| Second ACP guest | Optional |
| Extension/plugin API | Optional after at least three concrete integrations |
| Offline / local inference | Optional after a stated connectivity need |

Planning issues: [#119](https://github.com/Sannrox/rusui/issues/119)–[#140](https://github.com/Sannrox/rusui/issues/140) as opened.

## M6 Stable core

Freeze what was demonstrated in M1, M2, and M4. Security review and
contract freeze are required. M3's thirty-day observation and M5's
optional tracks are not required for the remote-workspace 1.0; a
maintenance or optional-capability release keeps its own gates.

Planning issues: [#141](https://github.com/Sannrox/rusui/issues/141)–[#146](https://github.com/Sannrox/rusui/issues/146).

## Core 1.0 operator and runtime scope

- Audience: one-maintainer self-hosting. Shared small-team use is M5.
  Hosted multi-tenant SaaS and billing are out of scope.
- Primary operator path: terminal CLI and HTTP+SSE. Browser console and
  editor ACP remain clients of the same API; neither is a core-1.0
  completeness gate.
- Merge authority: humans merge core delivery.
- Persistence: SQLite with WAL and one writer.
- Reasoning: ACP-owned guest loop; rusui owns environments, admission,
  credentials, evidence, and permitted external actions. P1 guest remains
  Grok CLI ([ADR 0002](docs/decisions/0002-grok-acp-agent-set.md)).

Explicitly deferred: native mobile clients, a new model-training effort,
a generalized workflow DSL, a plugin marketplace, transparent cross-
repository transactions, and unbounded autonomous self-modification of
policy or infrastructure.

## Assumptions

| # | Assumption | Verify | If false |
| --- | --- | --- | --- |
| A1 | Grok CLI keeps stdio ACP and `session/request_permission` under `--permission-mode default` | [ADR 0002](docs/decisions/0002-grok-acp-agent-set.md) | Drive shikigami through `serve` |
| A2 | A KVM-capable Linux host is available only if the optional microVM track is chosen | `ls /dev/kvm` on that host | Leave microVMs absent from core 1.0 |
| A3 | GitHub App tokens plus a git smart-HTTP proxy can enforce repository and branch scope | [ADR 0009](docs/decisions/0009-credential-broker.md) | Rulesets plus post-push verification |
| A5 | shikigami accepts an ACP server through its ADR process | Open the ADR | Drive shikigami through `serve`; weaker receipt capture |

## Counter-moves

| Move | Response |
| --- | --- |
| A vendor ships a self-hosted runner with a hosted plane | Differentiator remains operator-owned control state, agent choice, inspectable policy and receipts |
| Headless third-party use of Grok ACP is restricted | shikigami `serve`, later local model gateway as optional M5 |

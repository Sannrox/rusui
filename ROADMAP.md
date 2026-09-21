# Roadmap

> **Not the runbook.** Sequencing estimates, not deadlines or the product
> contract ([ARCHITECTURE.md](ARCHITECTURE.md)).
>
> [ADR 0010](docs/decisions/0010-hybrid-roadmap-sequence.md) is **Accepted**.
> This file is the published M1–M6 sequence. The earlier P0–P4 calendar
> remains at
> [94aabd7/ROADMAP.md](https://github.com/Sannrox/rusui/blob/94aabd7d292faa052ec8faba0f028f8e07bc9ade/ROADMAP.md).
> [ARCHITECTURE.md](ARCHITECTURE.md) names plane-owned `open_pr` /
> `update_pr` of a proven candidate ([ADR 0013](docs/decisions/0013-publication-authority.md)).

GitHub Issues remain the planning source of truth; this file links to them
and never overrides their `## Dependencies` sections. 24–36 months after
acceptance is a planning envelope, not a delivery commitment. Month zero
is acceptance of ADR 0010. Re-estimate after M1 and M2. Reduce scope
before moving a safety or correctness gate.

ADR 0010 does not enable live GitHub writes, implement-to-PR, or merging.
ADR 0013 names `open_pr` / `update_pr` of a proven candidate. Human merge
remains required.

## Shape

| Horizon | Operator outcome | Release shape |
| --- | --- | --- |
| **M1 Dependable sessions** | Leave one real session, return, steer it, recover interruptions | Maintainer alpha |
| **M2 Verified delivery** | One bounded issue becomes a verified PR; humans merge | Maintainer delivery beta |
| **M3 Continuous maintenance** | Two repositories stay reviewed; eligible repairs run under policy | Maintenance beta |
| **M4 Daily remote workspace** | CLI, editor, and console on the same API; independent install/restore | Self-hosting beta |
| **M5 Agent and infrastructure choice** | Independent optional tracks chosen from measured need | Selected capability releases |
| **M6 Stable core** | Freeze the demonstrated core; document support and limits | Candidate 1.0 |

Common path: M1 lifecycle → M2 publication → M3 maintenance promotion.
M4 may proceed from a stable M1/M2 API without waiting for every M3
promotion. M5 tracks are independent of each other. M6 freezes what was
demonstrated; it does not require every M5 track.

## M1 Dependable sessions

Exit: a bounded real-container/guest proof (twenty-run matrix including
follow-ups and deliberate faults). Process driver is not production
isolation. [ADR 0009](docs/decisions/0009-credential-broker.md) and
[ADR 0011](docs/decisions/0011-unattended-session-contract.md) stand.

Planning issues: [#88](https://github.com/Sannrox/rusui/issues/88)–[#95](https://github.com/Sannrox/rusui/issues/95).
Closed component issues are not this exit gate.

## M2 Verified delivery

May start after M1's bounded session proof **and** an accepted publication
contract. It does **not** wait for thirty unattended days. Humans merge.
[ADR 0013](docs/decisions/0013-publication-authority.md) is **Accepted**.
The product contract names `open_pr` / `update_pr` of a `proven` candidate.
Comment, close, merge, and land stay unauthorized. The publisher that
performs those writes is a later implementation issue.

Planning issues: [#96](https://github.com/Sannrox/rusui/issues/96)–[#103](https://github.com/Sannrox/rusui/issues/103).

## M3 Continuous maintenance

Retains the original P1 observation: thirty measured days on rusui plus
one sibling repository at twenty or more sessions per week with zero
observed credential leaks and no unauthorized action in the measured run.
A finite sample is evidence, not a future guarantee. Live comment/close/merge
are separate promotions, not required for the initial hybrid product.

Planning issues: [#104](https://github.com/Sannrox/rusui/issues/104)–[#110](https://github.com/Sannrox/rusui/issues/110).

## M4 Daily remote workspace

CLI and HTTP+SSE first. A small console subset may ship once the M1 API is
stable. The ten views, terminal, authenticated previews, and editor ACP
facade complete here ([ADR 0003](docs/decisions/0003-operator-surface.md)).
`rusui sync` remains unnecessary for unattended dogfood
([ADR 0006](docs/decisions/0006-session-start.md)).

Planning issues: [#111](https://github.com/Sannrox/rusui/issues/111)–[#118](https://github.com/Sannrox/rusui/issues/118).

## M5 Optional tracks (not required for core 1.0)

Each earlier gate stays named. Absence from core 1.0 is allowed only as
recorded in ADR 0010 D4.

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

Freeze what was demonstrated. Security review and contract freeze required.
Does not require every M5 track.

Planning issues: [#141](https://github.com/Sannrox/rusui/issues/141)–[#146](https://github.com/Sannrox/rusui/issues/146).

## Core 1.0 operator and runtime scope

- Audience: one-maintainer self-hosting. Shared small-team use is M5.
  Hosted multi-tenant SaaS and billing are out of scope.
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

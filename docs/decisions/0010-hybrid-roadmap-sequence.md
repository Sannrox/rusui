# ADR 0010: Hybrid core-1.0 sequence and earlier gate mapping

- Status: Proposed
- Date: 2026-09-20
- Resolves: [#87](https://github.com/Sannrox/rusui/issues/87)
- Amends: sequencing and phase-end measurements in
  [ADR 0001](0001-environment-plane.md) Validation, the P1 completeness
  calendar in [ADR 0003](0003-operator-surface.md), and the published
  [P0–P4 roadmap](../../ROADMAP.md). Does not amend ADR 0001 D1–D10,
  [ADR 0002](0002-grok-acp-agent-set.md),
  [ADR 0004](0004-rusui-services-yaml.md)–[0009](0009-credential-broker.md),
  or the current [ARCHITECTURE.md](../../ARCHITECTURE.md) contract.
- Discussion: none. GitHub Discussions are disabled; this pull request is
  the review venue. Merging with status Proposed records the investigation.
  Changing status to Accepted in the merged revision is the acceptance act.

## Context

The destination in [VISION.md](../../VISION.md) is a self-hosted environment
plane that can also keep configured repositories maintained. The published
roadmap is still the P0–P4 calendar from
[ADR 0001](0001-environment-plane.md): P1 dogfood after a thirty-day
unattended run, P2 orb-parity including microVM snapshot wake and a ship
lane, then fleet, HA, sharing, and a 1.0 freeze.

That calendar now collides with delivered work and with the product that
is actually needed next. Baseline
[94aabd7](https://github.com/Sannrox/rusui/tree/94aabd7d292faa052ec8faba0f028f8e07bc9ade)
already has project policy, per-session container environments, credential
proxies, run/schedule/follow-up, SSE attach, and an approval inbox. Apply
is still dry-run. Closed component issues are not the P1 thirty-day gate,
not a complete remote-session proof, and not permission to publish.

Two sequencing questions cannot be left implicit:

1. May a verified implementation-to-PR path (original P2.4) start before
   the original thirty-day P1 dogfood measurement?
2. May the ADR 0003 ten-view console and editor facade complete after a
   useful CLI/API session loop, rather than as a P1 exit requirement?

Original microVM/snapshot, fleet, HA, sharing, and extension gates are
still named in ADR 0001 and ROADMAP. They cannot be dropped by silence.
[#87](https://github.com/Sannrox/rusui/issues/87) asks for one explicit
map. Issues [#88](https://github.com/Sannrox/rusui/issues/88)–[#146](https://github.com/Sannrox/rusui/issues/146)
already describe a candidate M1–M6 path; they are planning items, not
accepted contract.

Competitive assumption A4 is stale: Amp documents customer-cloud
execution while keeping conversation state in Amp's service. Self-hosted
runners alone are not rusui's distinction. The remaining hypothesis is
operator-owned control state, agent choice, inspectable policy and
receipts, and reusable maintenance.

## Decision

Adopt one hybrid sequence through a deliberately smaller core 1.0.
24–36 months after acceptance is a planning envelope, not a delivery
commitment. GitHub Issues remain planning truth. This ADR does not
enable live GitHub writes, implement-to-PR, or merging.

### D1. Sequence

| Horizon | Operator outcome | Release shape |
| --- | --- | --- |
| **M1 Dependable sessions** | Leave one real session, return, steer it, recover interruptions | Private alpha |
| **M2 Verified delivery** | One bounded issue becomes a verified PR; humans merge | Personal delivery beta |
| **M3 Continuous maintenance** | Two repositories stay reviewed; eligible repairs run under policy | Maintenance beta |
| **M4 Daily remote workspace** | CLI, editor, and console on the same API; independent install/restore | Self-hosting beta |
| **M5 Agent and infrastructure choice** | Independent optional tracks chosen from measured need | Selected capability releases |
| **M6 Stable core** | Freeze the demonstrated core; document support and limits | Candidate 1.0 |

Common path: M1 lifecycle → M2 publication → M3 maintenance promotion.
M4 may proceed from a stable M1/M2 API without waiting for every M3
promotion. M5 tracks are independent of each other. M6 freezes what was
demonstrated; it does not require every M5 track.

Month zero is acceptance of this ADR. Re-estimate after M1 and M2.
Reduce scope before moving a safety or correctness gate.

### D2. M2 may precede the original thirty-day dogfood gate

**Yes.** Selected, plane-owned publication of a verified artifact may
start after M1's bounded session proof and an accepted publication
contract. It does not wait for thirty unattended days.

Rationale: the original P1 gate mixed "a session is dependable" with
"maintenance has been observed for a month." Verified delivery of
maintainer-selected fixes needs the former, plus a publication/trust
boundary. It does not need a month of advisory review.

The original measurement is **retained, not waived**:

- **M1 exit** is a bounded real-container/guest proof (a twenty-run
  matrix including follow-ups and deliberate faults). This is not the
  P1 thirty-day gate.
- **M3 exit** is the original P1 observation: thirty measured days on
  rusui plus one sibling repository at twenty or more sessions per week
  with zero observed credential leaks and no unauthorized action in the
  measured run. A finite sample is evidence, not a future guarantee.

ADR 0001's "Phase 1 ends after thirty unattended days…" therefore moves
to M3. Closing this issue, or merging this file as Proposed, does not
mark that gate complete.

M2 implementation still requires a later accepted amendment of the
dry-run / read-only GitHub contract and contributor restrictions. This
ADR is not that amendment.

### D3. Console and editor completeness move to M4

**Yes.** M1 and M2 accept on CLI and the HTTP+SSE API.
[ADR 0003](0003-operator-surface.md) stands: every surface is a view of
the same objects; Slack remains notify/approve; the console stack remains
Go templates, htmx, SSE, and bounded JS islands; rusui is not a chat app.

Calendar change only:

- A small console subset (sessions, session detail, approvals) may ship
  once the M1 API is stable. It must not delay the session proof.
- The ten views, terminal, authenticated previews, and editor ACP facade
  complete in M4.
- `rusui sync` remains unnecessary for unattended dogfood
  ([ADR 0006](0006-session-start.md)).

This amends ADR 0003's "P1.4 implements the API, CLI, ACP shim, and the
ten views above" as a phase-exit requirement. It does not add an eleventh
view or a new client stack.

### D4. Original optional gates are retained and not required for core 1.0

Each row stays a named earlier gate. Absence from core 1.0 is allowed
only as recorded here. Closing this issue does not waive them.

| Earlier gate | Treatment | Core 1.0 |
| --- | --- | --- |
| P0 wiring, identity, ACP, runner | Reconcile against merged work; remaining integration belongs to M1 | Required as current baseline plus M1 proof |
| P1 containers, guest, credential broker, event intake, budgets | M1 | Required. Process driver is not production isolation. [ADR 0007](0007-environment-snapshot.md) retention stands until amended. [ADR 0009](0009-credential-broker.md) trust boundary stands |
| P1.4 full console and editor facade | Early subset allowed; completeness in M4 | Required by M4, not by M1/M2 |
| P1 thirty-day dogfood | M3 observation requirement | Required for M3/M6; not an M2 entry gate |
| P2.4 ship lane | M2, after a publication contract | Required for M2, with human merge |
| P2.5 live review actions; P3.6 evaluation | M3; evaluate before promoting each action class | Advisory review required; live comment/close/merge are separate promotions and are not required for the initial hybrid product |
| P2 portals | M4, after auth, expiry, and revocation rules | Required by M4 |
| P2.1 microVM / snapshot wake under 2 s | Optional M5. Container stop/start is sufficient for M1 | **May remain absent** |
| P2.3 / P3.4 child-session delegation and live fork | Optional M5 | **May remain absent** |
| P3.1 runner fleet | Optional M5 | **May remain absent** |
| P3.2 Postgres and HA plane | Optional M5. SQLite-first until a measured RPO/RTO requires otherwise | **May remain absent** |
| P3.3 sekai-chisei governance; P3.5 shared sessions | Optional M5, only if multiple operators or required governance exist | **May remain absent** |
| P4.1 extension/plugin API | Optional after at least three concrete integrations need a stable extra boundary | **May remain absent** |
| P4.2 offline / local inference | Optional M5, only after a stated connectivity or data-boundary need | **May remain absent** |
| P4.4 security review and contract freeze | M6 | Required for 1.0 |

ADR 0001 Phase 2 "wake from snapshot under two seconds" is retained as
the M5 isolation-track candidate target on a named hardware/workload
profile. It is not a 1.0 requirement. "No pull request opens without a
recorded proof" becomes the M2 publication invariant after that contract
is accepted.

### D5. Core 1.0 operator and runtime scope

- **Audience.** One-maintainer self-hosting on the operator's hardware
  and repositories. The next audience is other self-hosting maintainers.
  Shared small-team use is an M5 branch. Hosted multi-tenant SaaS and
  billing are out of scope.
- **Merge authority.** Humans merge core delivery. Autonomous merge and
  issue-close are separate evaluated promotions, not M2 requirements.
- **Persistence.** SQLite with WAL and one writer remains the supported
  core. Postgres/HA wait on a measured recovery objective.
- **Reasoning.** ACP-owned: the guest owns the model loop; rusui owns
  environments, admission, credentials, evidence, and permitted external
  actions. P1 guest remains Grok CLI ([ADR 0002](0002-grok-acp-agent-set.md)).
  A second guest is optional M5.
- **Self-hosting proof.** M4 prefers one independent operator. If the
  product remains strictly personal, a clean-machine install and restore
  may substitute; external-user readiness then stays unclaimed.

Explicitly deferred: native mobile clients, a new model-training effort,
a generalized workflow DSL, a plugin marketplace, transparent cross-
repository transactions, and unbounded autonomous self-modification of
policy or infrastructure.

### D6. What acceptance does and does not authorize

Until this ADR is Accepted, the P0–P4 sequence in
[ROADMAP.md](../../ROADMAP.md) remains published, and
[ARCHITECTURE.md](../../ARCHITECTURE.md) stays the product contract.
A pointer from ROADMAP.md to this proposal is allowed. The ontology
must not record M1–M6 as implemented facts.

After acceptance:

- Rewrite ROADMAP.md to this sequence and keep a pointer to the P0–P4
  text at `94aabd7`.
- Do not rewrite ARCHITECTURE.md until a later accepted contract change
  (M2 publication is the first such candidate).
- Update the ontology only for definitions that actually change.

This ADR does not authorize live apply, implement-to-PR, scheduler
automations, or promising every optional track for 1.0.

## Consequences

Easier: M1 can finish the unattended session loop without waiting for a
ten-view console or a microVM. M2 can prove publication on selected
tasks before a month of maintenance observation. Optional infrastructure
stops blocking a useful 1.0.

Harder: ADR 0001/0003/ROADMAP phase labels diverge from calendar
reality until those documents are rewritten after acceptance. Dependent
issues that require an accepted G0 design stay blocked while this file
is Proposed. M2 still cannot start from this text alone.

Irreversible if accepted: the project will judge 1.0 without microVMs,
fleet, HA, sharing, or a plugin API unless a later ADR puts them back
into the freeze. The original gates remain named so that omission stays
an explicit choice.

## Rejected alternatives

- **Keep P0–P4 as the delivery calendar.** Cheapest on paper; forces
  microVMs, a full console, and a thirty-day gate in front of the first
  verified PR, after the plumbing for sessions already exists.
- **Waive original gates by publishing M1–M6 issues.** Issues are
  planning, not contract. Silence is not an amendment.
- **Require the thirty-day gate before any publication.** Protects
  observation; delays the hybrid loop the destination now asks for.
  Observation remains mandatory; it moves to M3.
- **Treat the ten-view console as an M1 exit.** Conflicts with
  "CLI/API first" and delays dependable sessions for screens that are
  not the session contract.
- **Declare microVM/fleet/HA/sharing/extensions required for 1.0.**
  Rebuilds the original 1.0 as orb-parity plus ecosystem, which this
  investigation was asked to shrink.
- **Hosted SaaS / billing as a 1.0 path.** Contradicts VISION non-goals
  and ADR 0001's self-hosted perimeter.

## Validation and reversal

Validation of this decision is maintainer review of this file. Validation
of the sequence is later milestone evidence, not this merge.

Reverse by superseding this ADR and restoring the P0–P4 calendar, or by
rejecting the pull request. No schema, policy, or runtime change is
implied, so there is no data migration to undo.

Unresolved after this decision: shikigami still needs an accepted ACP
server before it is a supported guest (original A5). A KVM host remains
required only for the optional microVM track, not for M1. Guest restore
and deferred-permission capabilities are D1 work
([#88](https://github.com/Sannrox/rusui/issues/88)), not this ADR.

## Sources

- [#87](https://github.com/Sannrox/rusui/issues/87); wayfinder map
  [#37](https://github.com/Sannrox/rusui/issues/37)
- Baseline
  [94aabd7](https://github.com/Sannrox/rusui/tree/94aabd7d292faa052ec8faba0f028f8e07bc9ade):
  [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ROADMAP.md](../../ROADMAP.md),
  [VISION.md](../../VISION.md),
  ADRs [0001](0001-environment-plane.md)–[0009](0009-credential-broker.md)
- Planning issues
  [#88](https://github.com/Sannrox/rusui/issues/88)–[#146](https://github.com/Sannrox/rusui/issues/146)
  (not accepted by this proposal)
- Amp Orbs and Amp self-hosted orbs documentation (read 2026-09-19);
  ClawSweeper orchestration and issue-to-PR documentation (read
  2026-09-19)

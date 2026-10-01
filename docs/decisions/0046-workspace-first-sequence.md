# ADR 0046: The supported core prioritizes a terminal-first remote workspace

- Status: Accepted
- Date: 2026-10-01
- Amends: [ADR 0010](0010-hybrid-roadmap-sequence.md) D1–D5. The core 1.0
  sequence and release gates change here. Its human-merge rule, bounded
  session and publication proofs, and D4 optional-track thresholds stand.
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) remains the product
  contract. This decision changes sequencing and support gates, not runtime
  authority.

## Context

ADR 0010 puts continuous maintenance before daily remote workspace and requires its thirty-day observation for M6. The operator's chosen product focus is a self-hosted remote workspace for coding agents, operated from the terminal. Maintenance automation is a workload of that workspace, not the prerequisite that makes it useful.

The D6 proof covers a bounded lifecycle but no live guest in its twenty-run matrix. The P8 ten-task publication pilot ended narrow. The U8 self-hosting journey was an in-process author exercise without an independent operator. The later unified live workflow was deferred after identity failures; its recorded fixes require a rerun. These results support further workspace and delivery proof before a broad 1.0 claim.

## Decision

1. The primary 1.0 outcome is: one self-hosting operator can install rusui, start a real managed coding-agent session from the terminal, leave and return, follow its transcript, steer and approve it, inspect the result, recover interruption, and obtain an attributable pull request. Humans merge.
2. Sequence the core as dependable managed sessions, bounded issue-to-PR delivery, a usable daily terminal workflow with independent self-hosting proof, then a stable-core freeze. Terminal usability work may proceed alongside delivery after the session API is stable. CLI and HTTP+SSE are the primary operator path. A browser console and editor facade may remain available, but completeness of either is not a core-1.0 gate when the equivalent supported terminal/API outcomes are proven.
3. Continuous maintenance, including the thirty-day two-repository observation, remains the required gate for a maintenance release or autonomous maintenance claim. It is not a prerequisite for the remote-workspace 1.0 freeze. Existing maintenance issues retain their own dependencies and promotion thresholds until explicitly reshaped. No live comment, close, merge, or land authority is granted.
4. MicroVMs, a runner fleet, HA, shared sessions, plugins, external identity, live fork, and same-session scheduled continuation remain optional, adoption-gated work. Existing trust, policy, credential, receipt, and human-merge constraints stand.
5. [ROADMAP.md](../../ROADMAP.md) records this sequence. Dependent planning
   Issues must be revised explicitly before their readiness changes. This
   decision does not mark a proof passed or rewrite ARCHITECTURE.md.

## Consequences

The first release claim becomes narrower and easier for another self-hosting maintainer to verify. Maintenance beta loses its place on the core critical path, while its safety and observation gates stay intact. The existing M1–M6 identifiers remain for Issue links; the core order is M1 → M2 → M4 → M6, with M3 and M5 as optional tracks. In particular, [#141](https://github.com/Sannrox/rusui/issues/141) still names [#110](https://github.com/Sannrox/rusui/issues/110) as a required dependency. It requires a separately authorized Issue update before a workspace-focused 1.0 can advance. Changing prose alone cannot make blocked issues ready. No schema, policy, API, runner, or credential migration follows from this decision.

## Rejected alternatives

- Retain M3 maintenance as a core-1.0 prerequisite: delays the stated remote-workspace outcome behind a separate autonomous-workload study.
- Copy all Amp Orbs features before 1.0: expands runtime and operator states beyond the measured single-host profile.
- Declare the workspace ready from closed component tickets: overstates the narrowed D6, P8, and U8 evidence.

## Validation and reversal

The maintainer accepted the workspace-first direction on 2026-10-01. Validate the sequence with a predeclared live managed-guest terminal workflow, a new bounded publication cohort, and an independent self-hosting operator exercise. A failed gate narrows the supported claim. Reverse the prioritization with a later ADR and corresponding roadmap and dependency mapping; no data migration is required.

## Sources

- [ADR 0010](0010-hybrid-roadmap-sequence.md),
  [ROADMAP.md](../../ROADMAP.md), and [VISION.md](../../VISION.md)
- [D6 session-workflow proof](../proofs/d6-session-workflow.md)
- [P8 ten-task pilot](../proofs/p8-pilot-results.md)
- [U8 operator journey](../proofs/u8-operator-journey.md)
- [Unified live workflow](../proofs/186-unified-workflow-results.md)

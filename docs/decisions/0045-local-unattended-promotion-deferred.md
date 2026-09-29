# ADR 0045: Local unattended promotion is deferred

- Status: Accepted
- Date: 2026-09-29
- Amends: none. [ADR 0016](0016-local-interactive-runtime.md) stands:
  local is experimental, default-off, human-driven.
  [ADR 0024](0024-session-surface.md) stands: Sumika attach is not the
  session UI.
- Resolves: [#186](https://github.com/Sannrox/rusui/issues/186)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0023](0023-p8-pilot-narrow.md) (P8 is narrow; it does not
  satisfy a pass gate),
  [#141](https://github.com/Sannrox/rusui/issues/141) (1.0 freeze still
  requires a passing unattended cohort),
  [docs/proofs/186-unified-workflow-matrix.md](../proofs/186-unified-workflow-matrix.md),
  [docs/proofs/186-unified-workflow-results.md](../proofs/186-unified-workflow-results.md).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[#186](https://github.com/Sannrox/rusui/issues/186) asked for a measured
comparison of one local Sumika session and one managed Rusui session
before the experimental local profile becomes part of the supported
core. Its observable outcome is a pass, narrow, or defer decision with
support limits. Its dependency on [#103](https://github.com/Sannrox/rusui/issues/103)
says that issue must **pass** before the combined workflow is promoted
as supported unattended product. [ADR 0023](0023-p8-pilot-narrow.md)
recorded that pilot as **narrow**.

The objects this choice binds:

| Object | Owner | Local profile | Managed profile |
| --- | --- | --- | --- |
| Session | plane | kind `local`; no Turn | kind `run` / `review` / `scheduled`; has Turns |
| Process | Sumika | live PTY child | none |
| Environment | plane | operator host; not an isolation boundary | container on this host |
| Attach | Sumika (local) / plane (managed) | steal the previous writer | write lease |
| PerTurnGrant | plane | none | one environment, one turn |
| Receipt | plane store | no PTY bytes | no PTY bytes |

The maintainer predeclared the matrix and thresholds on
[186-unified-workflow-matrix.md](../proofs/186-unified-workflow-matrix.md)
before results. Live execution required Sumika `3dadc7a` and an eval
plane on `127.0.0.1:8282`. The Sumika binary was not installed. Docker
was available. No eval plane was started.

Synthetic package tests at the candidate
(`TestLocalRuntimeHTTPAndSumikaLifecycle`) cover local create, attach,
steal, detach, process death, policy-bound argv, and operator-cannot-
restart. They are not the predeclared live campaign.

## Decision

**D1. Defer.** The experimental local profile is **not** promoted into
the supported unattended product. ADR 0016 remains: default-off,
human-driven, no Turns, no grants, no GitHub credential. Managed
sessions remain the unattended path.

**D2. Two profiles, one session surface.** The CLI and console open a
Session ([ADR 0024](0024-session-surface.md)). Sumika attach is the
local Process's own attach. The research does not require the two
profiles to present the same operator surface, and this ADR does not
make them one.

**D3. Adoption is a refused action until both gates hold:**

1. [#103](https://github.com/Sannrox/rusui/issues/103) has a **passing**
   unattended cohort (a superseding ADR to ADR 0023), which this issue
   requires before promotion; and
2. the predeclared live matrix is executed at an immutable revision
   with the named Sumika and managed runtimes, and meets the
   predeclared pass or narrow bar without an identity, authorization,
   or ownership failure.

**D4. Support limits until then:**

- Local: experimental. Policy must name `session_kinds: [local]` and
  `local_runtime`. Rusui never stores PTY bytes.
- Managed: the supported unattended path, within ADR 0023's recorded
  limits.
- #141 must not treat local as a supported 1.0 unattended claim.

No implementation follow-up is authorized. This ADR does not rewrite
[ARCHITECTURE.md](../../ARCHITECTURE.md).

## Consequences

Easier: one unattended product claim (managed), one experimental local
kind with a hard ownership split.

Harder: operators cannot treat Sumika local sessions as a supported
substitute for managed unattended work.

Irreversible: none.

## Rejected alternatives

- **Pass.** The live matrix did not run. Incomplete cells are not a
  pass.
- **Narrow.** Narrow requires one profile to pass fully live. Managed
  live cells were not run on the eval plane; local live cells could
  not start without Sumika.
- **Promote local anyway because synthetic tests pass.** The issue
  forbids hiding a failed or incomplete campaign as a pass. Synthetic
  tests are the adapter contract, not the predeclared cohort.

## Validation and reversal

Validation: this ADR is Accepted in the index; local remains absent
from policy defaults; `TestLocalRuntimeHTTPAndSumikaLifecycle` still
passes. Reverse by a superseding ADR that meets D3 and records the
live matrix results.

## Sources

- [#186](https://github.com/Sannrox/rusui/issues/186)
- [#184](https://github.com/Sannrox/rusui/issues/184),
  [#185](https://github.com/Sannrox/rusui/issues/185),
  [#95](https://github.com/Sannrox/rusui/issues/95),
  [#103](https://github.com/Sannrox/rusui/issues/103)
- [ADR 0016](0016-local-interactive-runtime.md),
  [ADR 0023](0023-p8-pilot-narrow.md),
  [ADR 0024](0024-session-surface.md)
- `internal/server/local_runtime_test.go`
  `TestLocalRuntimeHTTPAndSumikaLifecycle`

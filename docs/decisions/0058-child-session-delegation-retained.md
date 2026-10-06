# ADR 0058: Child-session delegation stays deferred

- Status: Accepted
- Date: 2026-10-06
- Amends: [ADR 0040](0040-child-session-delegation-deferred.md)
  (child-session delegation is deferred; one session, one environment).
- Resolves: [#489](https://github.com/Sannrox/rusui/issues/489)
- Related: [ADR 0006](0006-session-start.md),
  [ADR 0035](0035-live-environment-fork-deferred.md),
  [#121](https://github.com/Sannrox/rusui/issues/121),
  [#122](https://github.com/Sannrox/rusui/issues/122) (implementation
  remains blocked).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[ADR 0040](0040-child-session-delegation-deferred.md) deferred
child-session delegation. A parent cannot start other sessions. #122
is the implementation and stays unauthorized until a superseding ADR
meets ADR 0040 D3 and D4.

[#489](https://github.com/Sannrox/rusui/issues/489) asked whether to
adopt the bounded child-session decision already written on #121.
Two options:

1. Accept that decision and unblock #122 to implement one measured
   scenario.
2. Retain ADR 0040. #122 stays blocked.

No named workload is recorded whose independent child acceptance
checks beat a single-session baseline. ADR 0040 D3 required that
measurement before adoption. The #121 text is a later design
constraint, not that measurement.

Source evidence at the commit this decision was made against:

- `StartRun` creates one session. Listing the project after start
  returns that session only.
- `sessions` has `environment_id NOT NULL`. There is no
  `parent_session_id`, child table, or fan-out column.

## Decision

**D1. Retain the deferral.** rusui does not add a child-session,
parent-session, or delegation object. Independent work is another
session under ADR 0006. Follow-up on the same work is a new turn on
the same session.

**D2. Lineage is still not inferred.** Two sessions of the same
project share a project and a policy revision. Neither owns the
other.

**D3. #122 stays blocked** until a superseding ADR meets ADR 0040 D3
and D4 and amends ADR 0006 in the same change.

## Consequences

- Easier: one session, one environment, one grant, one lease meter.
- Harder: a task that wants parallel bounded children is split by
  the operator into independent sessions, or run as sequential turns.
- Schema, policy, runner contract, and public API are unchanged.

## Rejected alternatives

- **Adopt the #121 decision and unblock #122 (option 1).** Reopens
  ADR 0040 without the measured workload D3 required.

## Validation and reversal

Accept on merge. Validated when start still mints one session, and
`sessions` still has no parent column.

Reverse with a superseding ADR that meets ADR 0040 D3 and D4.

## Sources

- [#489](https://github.com/Sannrox/rusui/issues/489)
- [ADR 0040](0040-child-session-delegation-deferred.md)
- Decided against `main` at `5efaa12`.

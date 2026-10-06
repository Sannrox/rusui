# ADR 0056: A schedule fire still mints a new session

- Status: Accepted
- Date: 2026-10-06
- Amends: [ADR 0030](0030-schedule-new-session.md) (each fire is a new
  session; continuation is follow-up, not a schedule bind).
- Resolves: [#487](https://github.com/Sannrox/rusui/issues/487)
- Related: [ADR 0006](0006-session-start.md), [#81](https://github.com/Sannrox/rusui/issues/81)
  (operator follow-up), [#506](https://github.com/Sannrox/rusui/issues/506)
  (bind-to-session remains blocked).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[ADR 0030](0030-schedule-new-session.md) mints a new session per fire.
A schedule cannot wake the environment that already has the work. Its
reversal said to reopen only when a measured case needs the same
environment.

[#487](https://github.com/Sannrox/rusui/issues/487) asked whether
"continue this session on a schedule, same environment id" is that
case. Two options:

1. A schedule may bind to a session id. A fire wakes that environment
   and appends a prompt. An unbound schedule still mints a new session.
2. Retain ADR 0030. Continuation stays an operator follow-up (#81).

No measured case is recorded. "Continue this session" is the wish, not
the measurement ADR 0030 named. Follow-up on an existing run session
already appends a prompt in the environment that has the logs.

Source evidence at the commit this decision was made against:

- A fire in a new bucket after the previous session is no longer live
  mints a second session. It does not reuse the first session id.

## Decision

**D1. Retain new session per fire.** A schedule fire creates a new
session and a new environment. It does not bind to a session id.

**D2. Continuation stays follow-up.** Checking a long job in the
environment that already has logs is a follow-up turn (#81).

**D3. Bind-to-session stays refused** until a superseding ADR records
the measured case ADR 0030 asked for. [#506](https://github.com/Sannrox/rusui/issues/506)
remains blocked on that later choice.

## Consequences

- Easier: one schedule object. Skip-if-live still works.
- Harder: a cadence that must keep dirt in one environment uses
  follow-up, not a schedule.
- Schema, policy, runner contract, and public API are unchanged.

## Rejected alternatives

- **Bind a schedule to a session id (option 1).** Reopens ADR 0030
  without the measured case it required. Adds a bind, a wake, and a
  prompt-append on fire.

## Validation and reversal

Accept on merge. Validated when a later fire after the previous
session ends mints a new session id.

Reverse with a superseding ADR that records the measured same-
environment case.

## Sources

- [#487](https://github.com/Sannrox/rusui/issues/487)
- [ADR 0030](0030-schedule-new-session.md)
- Decided against `main` at `b9197f1`.

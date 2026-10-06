# ADR 0030: A schedule fire starts a new session

- Status: Accepted
- Date: 2026-09-28
- Amends: none. [ADR 0006](0006-session-start.md) scheduled start
  contract stands.
- Resolves: [#324](https://github.com/Sannrox/rusui/issues/324)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [#75](https://github.com/Sannrox/rusui/issues/75) (project schedules),
  [#81](https://github.com/Sannrox/rusui/issues/81) (follow-up turns).
- Amended by: [ADR 0056](0056-schedule-still-mints-a-new-session.md)
  (new session per fire; superseded by
  [ADR 0069](0069-schedule-binds-a-session.md), which lets a schedule
  bind to a session id; unbound fires still mint a new session).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

A named schedule fires a stored prompt. ADR 0006 already says each fire
is a new session, `delivery_id` is schedule id plus the period bucket,
and a live session from that schedule **skips** the fire. Operators who
want "keep working in the environment that has the logs" send follow-up
turns on a run session by hand.

The objects this choice binds:

| Object | Owner | Properties |
| --- | --- | --- |
| Schedule | project | name, UTC cadence, stored prompt |
| Session | plane | one environment; scheduled identity minted at fire |
| Turn | session | follow-up is a new turn on the same session |
| Delivery | plane | `sched/{id}/{bucket}`; duplicate is a no-op |

Links: a Schedule may start many Sessions over time; a Session is never
re-owned by a later fire. Follow-up is an action on Session, not on
Schedule.

## Decision

**D1. Keep new session per fire.** A schedule fire creates a new
session and a new environment. It does not wake, prompt, or attach to
an existing session. [ADR 0006](0006-session-start.md) is not amended.

**D2. Skip, pause, and delete stay as shipped.** If a session from that
schedule is still leased or running, skip. Pause (global or project)
stops new claims and new scheduled starts. Deleting the schedule stops
future fires; live sessions complete under existing lease rules.
Completion is the session's own terminal state, not a schedule
predicate.

**D3. Continuation is follow-up.** Checking a long job in the
environment that already has logs is a follow-up turn on that session
([#81](https://github.com/Sannrox/rusui/issues/81)). The schedule is
not that action.

**D4. Policy still allowlists kind.** A schedule cannot start a session
kind the project does not allow, and cannot broaden policy.

No implementation follow-up is authorized.

## Consequences

Easier: one start contract, one skip rule, one follow-up path.
Watchdogs that need dirt from a live environment use a run session and
follow-up. Morning sweeps stay a fresh session.

Harder: a cadence cannot by itself keep one environment alive.

Irreversible: none. No schema or policy field is added.

## Rejected alternatives

- **Wake and prompt the previous session.** Mixes schedule identity
  with session identity and changes overlap semantics.
- **Leave #324 open.** The research outcome is retain. Reopen with a
  superseding ADR when a measured watchdog requires schedule-owned
  continuation.

## Validation and reversal

Validation: `StepSchedules` in the same bucket and while live still
creates one session; this ADR is Accepted in the index. Reverse by a
superseding ADR that amends ADR 0006 in the same change.

## Sources

- [#324](https://github.com/Sannrox/rusui/issues/324)
- [ADR 0006](0006-session-start.md)
- `internal/engine/schedule.go` `StartScheduled`,
  `internal/store/store.go` `ScheduleHasLiveSession`

# ADR 0069: A schedule may bind to one session

- Status: Accepted
- Date: 2026-10-06
- Amends: [ADR 0006](0006-session-start.md) scheduled start.
  [ADR 0030](0030-schedule-new-session.md) still describes unbound
  fires.
- Supersedes: [ADR 0056](0056-schedule-still-mints-a-new-session.md)
- Resolves: [#487](https://github.com/Sannrox/rusui/issues/487)
  option 1
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (unchanged until
  rewritten), [#81](https://github.com/Sannrox/rusui/issues/81)
  (operator follow-up remains),
  [#492](https://github.com/Sannrox/rusui/issues/492) (queued prompt;
  delivered),
  [#506](https://github.com/Sannrox/rusui/issues/506) (implementation)
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[#487](https://github.com/Sannrox/rusui/issues/487) asked whether a
schedule may bind to a session id so a fire wakes that environment and
appends a prompt. [ADR 0056](0056-schedule-still-mints-a-new-session.md)
and [ADR 0030](0030-schedule-new-session.md) chose a new session per
fire.

The maintainer directed that rusui match a researched
operator-visible remote-environment product unless a rusui contract is
a total conflict. In that product a schedule belongs to one thread,
fires continue that thread, and the machine wakes with the same
context.

That product does not totally conflict with rusui. An unbound schedule
can still mint a new session. Operator follow-up (#81) remains.
[#492](https://github.com/Sannrox/rusui/issues/492) already queues a
prompt until the current turn ends, so a fire that finds a live turn
can wait instead of skipping or steering.

[#506](https://github.com/Sannrox/rusui/issues/506) is the
implementation. This ADR does not add schema.

## Decision

**D1. A schedule may bind to a session id.** A fire on a bound
schedule wakes that environment and appends the stored prompt as a
turn. The environment id is unchanged.

**D2. Unbound still mints a new session.** A schedule with no session
id keeps [ADR 0030](0030-schedule-new-session.md): each fire is a new
session and a new environment.

**D3. Overlap waits.** If the bound session has a live turn, the fire
queues the prompt and starts it when that turn ends
([#492](https://github.com/Sannrox/rusui/issues/492)). It does not
steer the live turn and it does not skip.

**D4. Delete stops later fires.** Deleting the schedule stops later
fires and does not cancel a running turn.

## Consequences

- Easier: a watchdog can keep working in the environment that already
  has logs.
- Harder: a bound session that never archives keeps receiving fires
  until the operator deletes the schedule or archives the session
  ([ADR 0063](0063-keep-environment-until-archive.md)).
- Admit and the schedule store change in #506. Policy files stay
  operator-owned.
- `ARCHITECTURE.md` changes only when that file is next rewritten.

## Rejected alternatives

- **New session per fire (option 2 / ADR 0030 and 0056).** Conflicts
  with the directed product shape. Not a total rusui-contract conflict
  that would keep it.

- **Skip while live.** Drops the fire the operator scheduled. The
  researched product waits.

- **Steer the live turn.** Changes in-flight work. #492 already
  distinguishes queued from steer.

## Validation and reversal

Accept on merge. Validated when #506 lands: one fire produces one new
turn on the same session and environment; an unbound schedule still
creates a new session; deleting the schedule stops later fires.

Reverse with a superseding ADR that restores new-session-per-fire.

## Sources

- [#487](https://github.com/Sannrox/rusui/issues/487)
- [ADR 0056](0056-schedule-still-mints-a-new-session.md)
- [ADR 0030](0030-schedule-new-session.md)
- [ADR 0006](0006-session-start.md)
- Decided against `main` at `4ed2882`.

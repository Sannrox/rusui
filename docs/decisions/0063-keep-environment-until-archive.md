# ADR 0063: Keep the environment until the operator archives it

- Status: Accepted
- Date: 2026-10-06
- Amends: [ADR 0007](0007-environment-snapshot.md) Lifetime. Idle
  72-hour expiry of the environment is withdrawn. Sleep still stops
  the container. The snapshot cache is unchanged.
- Supersedes: [ADR 0049](0049-idle-environment-expiry-retained.md)
- Resolves: [#480](https://github.com/Sannrox/rusui/issues/480)
  option 1
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (the idle-72-hour
  sentence stays until that file is rewritten),
  [ADR 0029](0029-single-host-runner.md) (one runner on the plane
  host),
  [#499](https://github.com/Sannrox/rusui/issues/499) (archive and
  unarchive verbs; stops idle destroy)
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[#480](https://github.com/Sannrox/rusui/issues/480) asked whether a
session the operator has not archived retains its environment across
weeks of sleep. [ADR 0049](0049-idle-environment-expiry-retained.md)
chose option 2: idle 72 hours still destroys the container; the next
turn rematerializes from the snapshot; in-container dirt is gone.

The maintainer directed that rusui match a researched
operator-visible remote-environment product unless a rusui contract is
a total conflict. In that product the machine pauses while idle, stays
until the operator archives the session, and resumes the same machine
on wake. Archive is the end of retention. A 72-hour window there is
snapshot reuse, not environment destroy.

That product does not totally conflict with rusui. Sleep and wake
already preserve dirt while the environment is live. [ADR 0007](0007-environment-snapshot.md)
already separates snapshot (prepared tree) from environment (running
machine). [ADR 0029](0029-single-host-runner.md) still bounds the
topology to one host; disk grows until the operator archives. That is
a cost, not a contract conflict.

[#499](https://github.com/Sannrox/rusui/issues/499) is the
implementation. This ADR does not add the verbs.

Source evidence at the commit this decision was made against:

- `store.EnvTTL` is `72 * time.Hour`.
- `ReapEnvironments` destroys a due handle and records `EnvExpired`.
- Sleep stops the container; wake restores the same environment id.

## Decision

**D1. Keep until archive.** A session the operator has not archived
retains its environment across sleep. The next turn uses that same
environment id and the in-container dirt.

**D2. Sleep still stops the container.** Sleep is pause, not destroy.
Wake starts the same handle.

**D3. Idle expiry of the environment is withdrawn.**
`ReapEnvironments` must not destroy an unarchived environment because
a TTL elapsed. The snapshot cache keyed by `source_hash` is a
different object; this ADR does not change how long a prepared tree
may be reused.

**D4. Archive ends retention.** `archive` sleeps the environment,
refuses new prompts, and stops schedules and webhooks aimed at that
session. `unarchive` allows prompts again. Both verbs belong to
[#499](https://github.com/Sannrox/rusui/issues/499). Until that issue
lands, this ADR is the accepted choice and option 1 of #480.

## Consequences

- Easier: a sleeping session still has its machine when the operator
  returns days later. Archive is a quiet state that still owns the
  session.
- Harder: one-host disk and container metadata grow until the
  operator archives. Operators who never archive accumulate stopped
  containers.
- Schema: archive state is added by #499. Policy, guest link, and
  publication are unchanged.
- `ARCHITECTURE.md` still states idle 72 hours. It changes only when
  that file is next rewritten.

## Rejected alternatives

- **Retain idle 72-hour expiry (option 2 / ADR 0049).** Destroys
  in-container dirt the operator did not ask to drop. Conflicts with
  the directed product shape. Not a total rusui-contract conflict
  that would keep it.

- **Destroy on archive.** Archive is pause-and-refuse, not delete.
  The session row and the environment id remain so unarchive can
  resume.

## Validation and reversal

Accept on merge. Validated when #499 lands: an unarchived sleeping
session keeps the same environment id and a dirty file past 72 hours;
archive refuses prompts; unarchive allows a follow-up on that
environment.

Reverse with a superseding ADR that restores idle destroy or that
deletes on archive.

## Sources

- [#480](https://github.com/Sannrox/rusui/issues/480)
- [ADR 0007](0007-environment-snapshot.md) Lifetime
- [ADR 0049](0049-idle-environment-expiry-retained.md)
- [ADR 0029](0029-single-host-runner.md)
- Decided against `main` at `8e4a6fa`.

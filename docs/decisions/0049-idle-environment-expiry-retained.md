# ADR 0049: Idle environment expiry remains 72 hours

- Status: Accepted
- Date: 2026-10-06
- Amends: [ADR 0007](0007-environment-snapshot.md) (Lifetime: idle 72h
  expires the environment; the session row stays; the next turn
  rematerializes from the snapshot).
- Resolves: [#480](https://github.com/Sannrox/rusui/issues/480)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (idle 72 hours from
  last wake or last turn end; unchanged until that file is rewritten),
  [ADR 0029](0029-single-host-runner.md) (one runner on the plane host),
  [#499](https://github.com/Sannrox/rusui/issues/499) (archive remains
  blocked while expiry still destroys the environment).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[ADR 0007](0007-environment-snapshot.md) sets `expires_at` to last wake
or last turn end, whichever is later. Idle 72 hours expires the
environment. Expire destroys the container. The session row stays. The
next turn rematerializes from the snapshot; in-container dirt is gone.
Sleep and wake already keep that dirt while the environment is live.

[#480](https://github.com/Sannrox/rusui/issues/480) asked whether a
session the operator has not archived should retain its environment
across weeks of sleep. Two options:

1. Keep the environment until the operator archives the session. Sleep
   still stops the container.
2. Retain the 72-hour expiry. A later turn rematerializes from the
   snapshot.

Option 1 needs an archive verb. That verb does not exist
([#499](https://github.com/Sannrox/rusui/issues/499) is blocked on this
research). [ADR 0029](0029-single-host-runner.md) keeps one runner on
the plane host, so keep-until-archive is unbounded disk and container
metadata on that host. ADR 0007's reversal named a missing pin and a
shared running environment; it did not name weeks of sleep as the
reopen condition.

Source evidence at the commit this decision was made against:

- `store.EnvTTL` is `72 * time.Hour`.
- `ReapEnvironments` lists rows whose `expires_at` is due, destroys
  the handle, and records `EnvExpired` with receipt "time to live
  elapsed".
- `local` does not expire.

## Decision

**D1. Retain idle expiry.** A session the operator has not archived
still loses its environment after 72 hours idle from last wake or last
turn end. Sleep still stops the container; it does not pause the clock.

**D2. Rematerialize, do not keep dirt.** After expiry the session row
stays. The next turn creates a new environment from the snapshot.
In-container dirt from the expired handle is gone.

**D3. Archive stays a later choice.** Keep-until-archive is refused
until a superseding ADR accepts unbounded host retention and names the
archive verb that would replace expiry. [#499](https://github.com/Sannrox/rusui/issues/499)
remains blocked on that later choice.

## Consequences

- Easier: one lifetime rule. Sleep, wake, and expire stay the existing
  three states. No archive object, no unbounded disk on the one-host
  runner.
- Harder: an operator who sleeps a session for more than 72 hours
  loses in-container dirt and waits for rematerialize on the next
  turn.
- Schema, policy, runner contract, trust model, and public API are
  unchanged.
- `ARCHITECTURE.md` still states the 72-hour idle sentence. It changes
  only when that file is next rewritten.

## Rejected alternatives

- **Keep until the operator archives (option 1).** Needs an archive
  verb that does not exist, and unbounded disk on the one-host runner
  ([ADR 0029](0029-single-host-runner.md)). Sleep already preserves
  dirt for the idle window the contract names.

## Validation and reversal

Accept on merge. Validated when `store.EnvTTL` is 72 hours and
`ReapEnvironments` leaves a live environment just before that TTL and
expires it after.

Reverse with a superseding ADR that accepts keep-until-archive, names
the archive verb, and bounds host retention.

## Sources

- [#480](https://github.com/Sannrox/rusui/issues/480)
- [ADR 0007](0007-environment-snapshot.md) Lifetime
- [ADR 0029](0029-single-host-runner.md)
- Decided against `main` at `2f74407`.

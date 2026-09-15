# ADR 0007: P1 environment snapshot identity

- Status: Accepted
- Date: 2026-09-15
- Amends: [ADR 0006](0006-session-start.md) (workspace of a session
  environment). [ADR 0001](0001-environment-plane.md) D4 (setup once
  per source hash; sleep = stop) and D8 (content-addressed snapshots
  on local disk) stand.
- Resolves: [#42](https://github.com/Sannrox/rusui/issues/42)
- Discussion: none. Merging with this status is the acceptance act.

## Context

The store has `source_hash`, 72h expiry, and driver hooks for
`.agents/setup` / `.agents/resume`, but `source_hash` has no meaning.
[ADR 0006](0006-session-start.md) made session and environment 1:1 and
said the workspace comes from the project's snapshot. Two sessions
must not share an environment; they may share a **snapshot** so setup
does not run again.

P1 default driver is container. Process remains tests/dev and does
not count toward dogfood.

## Decision

A **snapshot** is the prepared, reusable tree for a `source_hash`. An
**environment** is the running machine. Sharing a snapshot is not
sharing an environment.

**`source_hash`** is a digest of:

1. base image **identity** (resolved digest, not a moving tag),
2. the **git pin** SHA,
3. the bytes of `.agents/setup` if present.

P1 snapshots **require a git pin**. A project with no bound repo may
exist in policy and fails at environment create.

**Pins**

- **review pull:** PR head SHA.
- **review issue:** default-branch SHA at admit.
- **run / scheduled:** default branch of the project's bound repo
  (dogfood is 1:1).

**Prepare**

Lookup snapshot by `source_hash`. Miss: clone the pin into a throwaway
prepare container, run `.agents/setup` if present, record the
snapshot, then create the session environment from it (no setup).
Hit: create from the snapshot, skip setup. Wake of an existing
environment runs `.agents/resume` only.

The artifact lives in a **runner-local cache** keyed by `source_hash`.
The plane stores the hash string, not the bytes. P1 is one runner.

**Lifetime**

`expires_at` is last wake or last turn end, whichever is later. Idle
72h expires the environment. `local` does not expire and is not
dogfood.

Expire destroys the container. The **session row stays**. The next
turn **re-materializes** a new environment from the snapshot (setup
skipped on hit). Dirt in the old container is gone. Turns on a live
environment keep that dirt across sleep/wake.

A new git pin on a live session (new PR head, or default branch moved
at admit) **replaces the environment** for the new turn from the new
snapshot. Same session row, new environment. In-container dirt is
discarded. Setup runs only if that digest is a miss.

## Consequences

- Two review sessions on the same PR head skip setup the second time
  and still get two containers.
- A lockfile change is a new pin and therefore a new digest, so
  setup runs via snapshot miss, not `git fetch` inside a dirty
  environment.
- How the runner execs Grok into the container stays fog.

## Rejected alternatives

- **Git commit only.** Ignores base image changes.
- **Hash of the tree after setup.** Unknown before setup.
- **Image tag only.** Too coarse.
- **`git fetch` in the live container** on a new pin. Skips setup
  when deps changed.
- **Hard 72h from create.** Kills a hot PR that was idle then active.
- **Snapshot bytes on the plane.** The runtime is on the runner.

## Validation and reversal

Accept on merge. Reopen if a P1 session must start without a git pin,
or if two sessions must share a running environment.

## Sources

- [#42](https://github.com/Sannrox/rusui/issues/42)
- [ADR 0001](0001-environment-plane.md) D4, D8
- [ADR 0006](0006-session-start.md)
- `internal/env` Setup/Resume hooks and `source_hash` on `origin/main`

# ADR 0098: Snapshot-only clone retained

- Status: Accepted
- Date: 2026-11-20
- Amends: [ADR 0035](0035-live-environment-fork-deferred.md) (live
  environment fork stays deferred; D3 still unmet). ADR 0035 remains
  Accepted.
- Resolves: [#784](https://github.com/Sannrox/rusui/issues/784)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (product contract;
  this ADR does not rewrite it),
  [ADR 0001](0001-environment-plane.md) D4,
  [ADR 0007](0007-environment-snapshot.md) (a snapshot is the only
  clone source),
  [ADR 0028](0028-container-isolation-profile.md) (container sleep
  keeps disk, not process memory),
  [ADR 0071](0071-bounded-child-session.md) (a child starts from a
  snapshot),
  [#325](https://github.com/Sannrox/rusui/issues/325)
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act. Until merge this is a proposal, not
  implementation authority. This ADR does not add `Fork`, schema, or
  policy.

## Context

[ADR 0035](0035-live-environment-fork-deferred.md) deferred live
environment fork: a second session on a copy of a running
environment's files and, at most, its processes. Its D3 gate requires
the maintainer to name one task that must continue from a running
environment's uncommitted state, and to show that committing that
state to the session branch and starting from a snapshot does not
serve it. A superseding ADR must then select what is copied, what
owns the child environment, and how grants rebind.

[#784](https://github.com/Sannrox/rusui/issues/784) reopened the
question after child sessions shipped. Two options:

1. Adopt copy-on-create from live files: a new session and a new
   environment from the parent's current workspace files, not its
   processes. Grants, leases, and preview bindings do not copy.
2. Retain snapshot-only clone. ADR 0035 stands.

No named task meets D3. Authorizing this research did not name one.
[ADR 0071](0071-bounded-child-session.md) already starts a child from
a snapshot; uncommitted parent dirt stays on the parent unless an
explicit file-copy action writes a receipt. The supported isolation
profile is still the container ([ADR 0028](0028-container-isolation-profile.md)):
sleep is stop; it keeps disk, not process memory. Process checkpoint
is not part of that profile.

Source evidence at the commit this decision was made against
(`6591023ca78a65c2508c09bcc99bad1767f56863`):

- The driver interface is `Kind`, `Create`, `Sleep`, `Wake`,
  `Destroy` (`internal/env/driver.go`). There is no `Fork`,
  `Snapshot`, or `Restore` on it.
- Container sleep is runtime stop; wake is start
  (`internal/env/container.go`). No checkpoint path exists on the
  driver.
- `StartChildGuest` (`internal/engine/child.go`) admits a new
  session, records `child.spawn`, and pins the child to the default
  branch SHA (or the project key). It does not copy the parent's
  live workspace.
- Child create still writes a snapshot item, not a live tree.

ADR 0035 D1, D2, and D4 stand. D3's first gate is still unmet.

## Decision

**D1. Retain snapshot-only clone.** rusui has no live environment
fork. A second session starts from a snapshot
([ADR 0007](0007-environment-snapshot.md)), never from another
session's running environment. `Fork` in ADR 0001 D4 is still not a
driver operation.

**D2. Child sessions are not a fork.** [ADR 0071](0071-bounded-child-session.md)
already covers a parent starting a bounded child. That child is a new
session and a new environment from a snapshot. This ADR does not
replace that path and does not copy uncommitted parent files into it.

**D3. ADR 0035 D3 still holds.** Adoption stays a refused action
until both of its gates hold. This research did not name the task
gate 1 requires. Dated reason, 2026-11-20: no task is named that must
continue from a running environment's uncommitted state, and
committing to the session branch then starting from a snapshot is
still unrefuted for every named need.

**D4. No implementation follow-up.** This ADR adds no driver method,
schema, policy field, or copy machinery. A later ADR that meets D3
may adopt a copy-only fork under ADR 0035 D4; this one does not
authorize that work.

## Consequences

Easier: one clone source, one grant binding per environment, and no
copy-on-create or checkpoint machinery on the container driver. Child
session create stays the fan-out path.

Harder: trying two approaches on the same dirty tree still needs the
operator to commit to the session branch first, then start a second
session from that pin. Running processes and in-memory state are not
carried over.

Irreversible: none. Schema, policy, runner contract, trust model, and
public API are unchanged.

[ARCHITECTURE.md](../../ARCHITECTURE.md) still omits live fork. This
ADR is not that rewrite.

## Threat examples

Retaining the deferral keeps each of these unintroduced. An adopting
ADR must still answer ADR 0035 D4.

| Attempt | Result |
| --- | --- |
| Two sessions write one workspace | Refused. D1: the child is a snapshot clone, not a shared tree. |
| Copy live files without naming a task | Refused. D3: ADR 0035 D3 gate 1 is unmet. |
| Copy processes with the files | Refused. The container profile has no checkpoint. D4 of this ADR authorizes no driver op. |
| Child inherits parent grant, lease, or preview | Refused. ADR 0035 D4; child sessions mint their own ([ADR 0071](0071-bounded-child-session.md)). |
| Guest receives a core credential via copied files | Refused. Grants do not copy. Guests never receive a core credential. |

## Rejected alternatives

- **Adopt copy-on-create from live files now.** D3 is unmet. Child
  sessions already start from a snapshot. Copying the live tree
  would add a second clone source, a quiesce point, and grant-rebind
  rules without a named task that commit-then-snapshot cannot serve.
- **Two sessions on one environment.** Issue non-goal. Two writers on
  one tree. ADR 0035 D4 forbids a shared writable workspace.
- **Copy processes as well as files.** Needs checkpoint support the
  container profile does not have. ADR 0028 would have to change
  first.
- **Replace child sessions from snapshots with live fork.** Children
  already exist ([ADR 0071](0071-bounded-child-session.md)). Fork is a
  different object: it starts from live dirt, not a pin.
- **Reject live fork outright.** ADR 0035 already chose deferral so a
  copy-only design that keeps D4 can return if D3 holds. This retain
  keeps that door, dated.

## Validation and reversal

Validation: this ADR is Accepted in the index; ADR 0035 remains
Accepted and lists this amendment; `env.Driver` has no `Fork`; a
child session environment is created from a snapshot. Reverse by a
superseding ADR that meets ADR 0035 D3 and D4 and amends ADR 0001 D4
and ADR 0007 in the same change. Do not delete this file.

## Sources

- [#784](https://github.com/Sannrox/rusui/issues/784)
- [#325](https://github.com/Sannrox/rusui/issues/325)
- [ADR 0001](0001-environment-plane.md) D4,
  [ADR 0007](0007-environment-snapshot.md),
  [ADR 0028](0028-container-isolation-profile.md),
  [ADR 0035](0035-live-environment-fork-deferred.md),
  [ADR 0071](0071-bounded-child-session.md)
- `internal/env/driver.go` `Driver`;
  `internal/env/container.go` `Sleep` / `Wake`;
  `internal/engine/child.go` `StartChildGuest`
- Commit `6591023ca78a65c2508c09bcc99bad1767f56863`

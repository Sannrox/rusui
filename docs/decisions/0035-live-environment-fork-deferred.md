# ADR 0035: Live environment fork is deferred

- Status: Accepted
- Date: 2026-09-28
- Amends: [ADR 0001](0001-environment-plane.md) D4 (`Fork` on the
  driver) and [ADR 0007](0007-environment-snapshot.md) (a snapshot is the
  only clone source).
- Resolves: [#325](https://github.com/Sannrox/rusui/issues/325)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0009](0009-credential-broker.md) (per-turn grant),
  [ADR 0010](0010-hybrid-roadmap-sequence.md) (live fork may remain
  absent), [ADR 0012](0012-operator-access.md) (terminal and preview
  grants), [ADR 0028](0028-container-isolation-profile.md) (container
  isolation), [#121](https://github.com/Sannrox/rusui/issues/121) and
  [#122](https://github.com/Sannrox/rusui/issues/122) (child-session
  delegation, a sibling question).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[ADR 0001](0001-environment-plane.md) D4 listed `Fork` among the driver
operations. [#121](https://github.com/Sannrox/rusui/issues/121) groups
"child-session delegation and live fork" as one optional track. They are
different objects:

| Operation | Creates | Starts from | Question owned by |
| --- | --- | --- | --- |
| Child session | a new Session and a new Environment | a snapshot | #121 |
| Live fork | a second Session on a copy of a running Environment | the parent's files and, at most, its processes | this ADR |
| Create from snapshot | a new Environment | the prepared tree for a `source_hash` | ADR 0007 |

[#325](https://github.com/Sannrox/rusui/issues/325) asked whether live
fork is a distinct operation, or whether create from a snapshot is
always enough. Its adoption gate requires the maintainer to name one
task that must share a live tree rather than a snapshot. None is named.

Source evidence at the commit this decision was made against:

- The driver interface is `Kind`, `Create`, `Sleep`, `Wake`, `Destroy`
  (`internal/env/driver.go`). There is no `Fork`, `Snapshot`, or
  `Restore` on it.
- The supported isolation profile is the container
  ([ADR 0028](0028-container-isolation-profile.md)). Sleep is container
  stop; it keeps disk, not process memory. A container runtime can copy a
  filesystem; copying live processes needs checkpoint support that is
  not part of this profile.
- [ADR 0010](0010-hybrid-roadmap-sequence.md) lets live fork remain
  absent from the 1.0 core.
- Grants bind to one environment: the per-turn grant
  ([ADR 0001](0001-environment-plane.md) D3), the terminal write lease
  and preview grant ([ADR 0012](0012-operator-access.md)), and the
  session ref prefix `refs/heads/rusui/<session>/*`
  ([ADR 0009](0009-credential-broker.md)).

## Decision

**D1. Defer.** rusui has no live environment fork. `Fork` in ADR 0001 D4
is not a driver operation. A second session starts from a snapshot
([ADR 0007](0007-environment-snapshot.md)), never from another session's
running environment.

**D2. Child sessions stay separate.** This ADR does not decide
delegation. [#121](https://github.com/Sannrox/rusui/issues/121) keeps
new-session delegation; a child session gets a new environment from a
snapshot under that decision.

**D3. Adoption is a refused action until both gates hold:**

1. the maintainer names one task that must continue from a running
   environment's uncommitted state, and shows that committing the state
   to the session branch and starting from a snapshot does not serve it;
   and
2. a superseding ADR selects what is copied, what owns the child
   environment, and how grants rebind, within the container profile or
   a profile a superseding ADR to ADR 0028 selects.

**D4. Constraints any adopted design must meet:**

- **No shared writable tree.** The child gets a copy. Two sessions never
  write the same workspace.
- **No broader grant.** The child's grant, repo set, and ref prefix are
  its own and no wider than the parent's. It pushes only under its own
  `refs/heads/rusui/<child-session>/*`.
- **Nothing rebinds.** Terminal leases, preview grants, and the per-turn
  grant are never copied. The child mints its own. A parent handle in a
  copied file or process resolves to nothing for the child.
- **Source state.** Fork of a running or sleeping environment is a copy
  at a quiesced point. Fork of a replaced, expired, or cancelled
  environment is refused.
- **Lifetime.** Cancel and expiry of the parent do not cancel the child,
  and the reverse.
- **Lineage.** The store records parent session, parent environment, and
  fork time.

No implementation follow-up is authorized. This ADR adds no driver
method, schema, or policy field.

## Consequences

Easier: one clone source, one grant binding per environment, and no
copy-on-write or checkpoint machinery on the container driver.

Harder: trying two approaches on the same dirty tree needs the operator
to commit to the session branch first, then start a second session from
that pin. Running processes and in-memory state are not carried over.

Irreversible: none.

## Threat examples

The deferral removes each of these. An adopting ADR must answer each.

- **Two writers on one tree.** A fork that shares the parent's writable
  workspace lets two agents overwrite each other with no one-writer
  rule. D4 requires a copy.
- **Fork used to escape a path fence or ref prefix.** A child that
  inherits the parent's grant, or a broader project, pushes where the
  parent could not, or outside its own session prefix. D4 keeps the
  grant no wider and the prefix the child's own.
- **Leaked terminal or preview grant.** A copied cookie, lease token, or
  preview URL in the child still resolves to the parent's handle, so a
  viewer of the child drives the parent. D4 forbids copying any grant.

## Rejected alternatives

- **Accept live fork now.** No task is named, the driver has no
  operation for it, and container stop keeps disk, not processes.
  Copying processes would need a runtime profile ADR 0028 did not select.
- **Reject live fork outright.** A copy-only fork that keeps D4 may be
  cheap on the container driver once a task needs it. Deferral keeps it
  open behind D3.
- **Specify fork inside #121.** It would either underspecify fork or
  overload delegation. The objects differ.

## Validation and reversal

Validation: this ADR is Accepted in the index; `env.Driver` has no
`Fork`; a new session environment is created from a snapshot. Reverse by
a superseding ADR that meets D3 and D4 and amends ADR 0001 D4 and ADR
0007 in the same change.

## Sources

- [#325](https://github.com/Sannrox/rusui/issues/325)
- [#121](https://github.com/Sannrox/rusui/issues/121),
  [#122](https://github.com/Sannrox/rusui/issues/122)
- [ADR 0001](0001-environment-plane.md) D3, D4,
  [ADR 0007](0007-environment-snapshot.md),
  [ADR 0009](0009-credential-broker.md),
  [ADR 0010](0010-hybrid-roadmap-sequence.md),
  [ADR 0012](0012-operator-access.md),
  [ADR 0028](0028-container-isolation-profile.md)
- `internal/env/driver.go` `Driver`

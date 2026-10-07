# ADR 0071: Bounded child-session delegation

- Status: Accepted; amended by [ADR 0072](0072-cross-project-child-session.md), [ADR 0073](0073-session-plane-guest-registry.md)
- Date: 2026-10-06
- Amends: [ADR 0006](0006-session-start.md). A session may start
  bounded children. Each child is a new session and a new environment.
- Supersedes: [ADR 0040](0040-child-session-delegation-deferred.md),
  [ADR 0058](0058-child-session-delegation-retained.md)
- Resolves: [#489](https://github.com/Sannrox/rusui/issues/489)
  option 1; accepts the bounded decision recorded on
  [#121](https://github.com/Sannrox/rusui/issues/121)
- Related: [ADR 0035](0035-live-environment-fork-deferred.md) (live
  fork stays deferred),
  [#135](https://github.com/Sannrox/rusui/issues/135) (cross-project
  children: [ADR 0072](0072-cross-project-child-session.md)),
  [#122](https://github.com/Sannrox/rusui/issues/122) (implementation)
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[#489](https://github.com/Sannrox/rusui/issues/489) asked whether to
accept the bounded child-session decision already written on #121.
[ADR 0040](0040-child-session-delegation-deferred.md) and
[ADR 0058](0058-child-session-delegation-retained.md) deferred it.

The maintainer directed that rusui match a researched
operator-visible remote-environment product unless a rusui contract is
a total conflict. In that product an agent starts other agents, each
with its own conversation, context, working copy, and machine. Files
do not share uncommitted state automatically. Transfers are explicit.
The parent continues. Fan-out is allowed.

That product does not totally conflict with rusui when bounded as
#121 already wrote: each child is a new session and a new environment
from a snapshot, not a live fork ([ADR 0035](0035-live-environment-fork-deferred.md)).
Children do not share a writable workspace. A child cannot add a
project, repository, session kind, or egress class the parent lacks.
Cross-project authority stays [#135](https://github.com/Sannrox/rusui/issues/135).

[#122](https://github.com/Sannrox/rusui/issues/122) is the
implementation. This ADR does not add schema. #122 may still carry
other dependencies of its own.

## Decision

**D1. Accept the #121 bound.** A parent session may start child
sessions. Each child has its own environment. Children do not share a
writable workspace unless an explicit file-copy action writes a
receipt. Fan-out, depth, and `max_concurrent_leases` apply to the
aggregate. A child cannot add a project, repository, session kind, or
egress class the parent lacks. Parent cancel cancels children. Child
failure is a recorded result on the parent. Result collection is a
durable message plus an optional file copy.

**D2. Not a live fork.** A child starts from a snapshot
([ADR 0035](0035-live-environment-fork-deferred.md)). Uncommitted
parent dirt is not copied unless D1's file-copy action runs.

**D3. Not cross-project authority.** A child of a session on project A
does not gain project B. That question stays #135.

**D4. Independent work is still allowed.** Two sessions of the same
project with no parent remain two sessions under ADR 0006. This ADR
adds lineage; it does not require it.

## Consequences

- Easier: a parent can fan out bounded work and keep going.
- Harder: admit, budget, cancel, and recovery now have a tree.
  Operators must read child receipts to know the outcome.
- Admit and worker change in #122. Policy overlay may only narrow.
- `ARCHITECTURE.md` changes only when that file is next rewritten.

## Rejected alternatives

- **Keep the deferral (option 2 / ADR 0040 and 0058).** Conflicts with
  the directed product shape. Not a total rusui-contract conflict
  when D1–D3 hold.

- **Shared writable workspace.** Issue non-goal. Live fork by another
  name.

- **Cross-project children in this ADR.** Issue non-goal. #135.

## Validation and reversal

Accept on merge. Validated when #122 lands one measured parent/child
scenario inside D1. Until then this ADR is the accepted choice and
option 1 of #489.

Reverse with a superseding ADR that drops parent/child objects.

## Sources

- [#489](https://github.com/Sannrox/rusui/issues/489)
- [#121](https://github.com/Sannrox/rusui/issues/121)
- [ADR 0040](0040-child-session-delegation-deferred.md)
- [ADR 0058](0058-child-session-delegation-retained.md)
- Decided against `main` at `27b6388`.

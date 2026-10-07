# ADR 0072: Bounded cross-project child sessions

- Status: Accepted
- Date: 2026-10-07
- Amends: [ADR 0071](0071-bounded-child-session.md) D3. A parent may
  start a child on another project the operator already has. The child
  is admitted under that project's policy.
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (unchanged until
  rewritten), [ADR 0005](0005-policy-v2-project.md) (policy stays
  project-keyed; overlay may only narrow),
  [ADR 0035](0035-live-environment-fork-deferred.md) (live fork stays
  deferred),
  [#136](https://github.com/Sannrox/rusui/issues/136) (implementation)
- Resolves: [#135](https://github.com/Sannrox/rusui/issues/135)
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[#135](https://github.com/Sannrox/rusui/issues/135) asked for one
bounded cross-repository parent plan: an accountable parent, independently
reviewable outputs, and no implicit grant propagation.
[ADR 0071](0071-bounded-child-session.md) D3 left that question here.

The maintainer directed that rusui match a researched
operator-visible remote-environment product unless a rusui contract is
a total conflict. In that product a parent agent names the project the
new thread runs in, so work can move to a different repository. The
new thread has its own conversation, working copy, and machine.
Uncommitted files are not shared. Transfers are explicit. Each
repository keeps its own publication. The parent continues.

That shape does not totally conflict with rusui when the child is a
new session on a project the operator already operates. The operator
token can already start a session on any configured project
([ADR 0012](0012-operator-access.md)). Lineage is the new object. The
child does not inherit the parent's repository grants, session kind, or
egress class. The parent does not gain the child's. Overlay still may
only narrow ([ADR 0005](0005-policy-v2-project.md)). Live fork stays
deferred ([ADR 0035](0035-live-environment-fork-deferred.md)). Atomic
cross-repository merge stays a non-goal.

The concrete task that is insufficient as two unlinked sessions: a
parent session on this repository starts one child on the pinned ACP
guest repository to land a protocol or docs change that must be
reviewed on its own. Bound: two projects, depth 1, explicit file
transfer, independently reviewable outputs (or a blocked reason) on
each repository. Operator effort is one parent prompt plus two
reviews instead of two manually coordinated sessions with no shared
cancel or result.

[#136](https://github.com/Sannrox/rusui/issues/136) is the
implementation. This ADR does not add schema.

## Decision

**D1. A parent may name one existing operator project as the child's
project.** Omitted keeps today's same-project child ([ADR 0071](0071-bounded-child-session.md)).
The named project must already exist in policy for this operator. The
plane does not create a project, repository, or grant from the
request.

**D2. The child is admitted under the named project's policy.** Budget,
tools, ship, guest, and overlay of that project apply. The parent
session's grants do not authorize work in the child. The child's
grants do not authorize work in the parent. A child still cannot add
a repository, session kind, or egress class that named project lacks.

**D3. Publication stays per repository.** A child that is an
implement session publishes under the named project's `ship` field
([ADR 0068](0068-project-ship-behavior.md)): omitted or
`pull-request` opens or updates a pull request; `push-base` pushes
the default branch and does not open a pull request. Review and
ordinary run stay read-only on GitHub. A child that cannot publish
records a blocked reason. The parent does not merge, close, or
comment on the child's item. Live merge, comment, and close stay
unauthorized.

**D4. Transfers stay explicit.** Uncommitted parent dirt is not copied.
A file-copy action writes a receipt, as in ADR 0071 D1. Result
collection is a durable message plus an optional file copy.

**D5. The tree bound still holds.** Fan-out, depth 1, parent cancel,
child failure as a parent result, and `max_concurrent_leases` on the
aggregate stay as ADR 0071 D1. Two projects do not raise depth.

## Consequences

- Easier: a parent can hand a docs or pin change to the guest
  repository and keep working, with one cancel and one result tree.
- Harder: admit must name the child's project, account budget on both
  projects and on the tree, and refuse implicit grant propagation.
- Admit and worker change in #136. Policy overlay may only narrow.
- `ARCHITECTURE.md` changes only when that file is next rewritten.

## Rejected alternatives

- **Keep ADR 0071 D3 (same-project children only).** Conflicts with
  the directed product shape. Not a total rusui-contract conflict
  when D1–D5 hold.

- **Implicit grant propagation.** Issue non-goal. A child on project B
  does not receive project A's repositories, tools, or egress.

- **Atomic cross-repository transactions or coordinated releases.**
  Issue non-goals. Each repository stays independently reviewable.

- **A child that mints a project the operator does not already have.**
  Would create authority the operator token did not already hold.

## Validation and reversal

Accept on merge. Validated when #136 lands the two-project scenario
in Context: parent on this repository, one child on the pinned ACP
guest repository, explicit file transfer, independently reviewable
outputs under each project's `ship` field. Until then this ADR is
the accepted choice for #135.

Reverse with a superseding ADR that drops the named-project child
object and restores ADR 0071 D3.

## Sources

- [#135](https://github.com/Sannrox/rusui/issues/135)
- [ADR 0071](0071-bounded-child-session.md)
- [ADR 0005](0005-policy-v2-project.md)
- Decided against `main` at `250c613`.

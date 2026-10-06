# ADR 0040: Child-session delegation is deferred

- Status: Accepted; amended by 0058
- Date: 2026-09-29
- Amends: none. [ADR 0006](0006-session-start.md) stands: one session,
  one environment. [ADR 0035](0035-live-environment-fork-deferred.md)
  stands: a second session starts from a snapshot.
- Amended by: [ADR 0058](0058-child-session-delegation-retained.md).
  Child-session delegation stays deferred. #122 stays blocked.
- Resolves: [#121](https://github.com/Sannrox/rusui/issues/121)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0001](0001-environment-plane.md) D1 and D4,
  [ADR 0005](0005-policy-v2-project.md) (budget and overlay are
  project-scoped; overlay may only narrow),
  [ADR 0006](0006-session-start.md),
  [ADR 0009](0009-credential-broker.md) (per-turn grant binds one
  environment),
  [ADR 0010](0010-hybrid-roadmap-sequence.md) (child-session
  delegation may remain absent from the 1.0 core),
  [ADR 0035](0035-live-environment-fork-deferred.md) (live fork is a
  sibling question, already deferred),
  [#122](https://github.com/Sannrox/rusui/issues/122) (implementation of
  a delegation workflow stays unauthorized).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[#121](https://github.com/Sannrox/rusui/issues/121) asked whether a
session may start bounded child sessions: parent/child lineage, fan-out
and depth, shared budget reservations, cancellation, recovery, and
result collection. Its observable outcome is an accepted or rejected
delegation design, or an explicit deferral with evidence. Its adoption
gate requires the maintainer to select the track after repeatable work
demonstrates a benefit from decomposition. No such workload is named.

Child sessions are not live fork. [ADR 0035](0035-live-environment-fork-deferred.md)
already refused copying a running environment. A child session would be
a **new Session and a new Environment** created from a snapshot under
a parent. That object does not exist today.

The objects this choice binds:

| Object | Owner | Properties |
| --- | --- | --- |
| Session | plane | one project, one environment; kinds `review`, `run`, `scheduled` (experimental `local`) |
| Environment | plane | one session; created or restored at session create |
| Turn | plane | one leased attempt on that session |
| PerTurnGrant | plane broker | bound to one environment |
| Project budget | policy | `max_concurrent_leases` (default 1); not reserved per parent |

Links: a Session uses one Environment; a Turn belongs to one Session;
a grant belongs to one Turn and one Environment. There is no parent
Session.

Source evidence at the commit this decision was made against:

- [ADR 0006](0006-session-start.md): object graph
  `project → environment → session → turn`. **One session, one
  environment.** A follow-up prompt is a new turn on the same session.
  Two review items are two environments. A schedule fire that finds a
  live session from that schedule skips; it does not spawn children.
- `sessions.environment_id` is `NOT NULL`. There is no
  `parent_session_id`, child table, or fan-out column
  (`internal/store/migrate.go`).
- The concurrent-lease meter is project
  `budgets.max_concurrent_leases` (default 1)
  ([ARCHITECTURE.md](../../ARCHITECTURE.md)). Unnamed budget keys fail
  closed. There is no reservation a parent can hold for children.
- Per-turn grants, terminal write leases, preview grants, and
  `refs/heads/rusui/<session>/*` bind one environment
  ([ADR 0009](0009-credential-broker.md),
  [ADR 0012](0012-operator-access.md)).
- [ADR 0010](0010-hybrid-roadmap-sequence.md) lists child-session
  delegation as optional M5 that may remain absent.

No decomposed-task cohort is recorded against a single-session
baseline. Coordination cost, outcome quality, and operator effort for
parent-started children are unmeasured.

## Decision

**D1. Defer.** rusui does not add a child-session, parent-session, or
delegation object in the 1.0 core. Independent work is another session
created under ADR 0006, from a snapshot under ADR 0007. Follow-up on
the same work is a new turn on the same session, not a child.

**D2. Lineage is not inferred.** Two sessions of the same project are
siblings only in the sense that they share a project and a policy
revision. Neither owns the other. Cancel, pause, and lease expiry apply
to the session they name.

**D3. Adoption is a refused action until both gates hold:**

1. the maintainer names one repeatable workload whose independent
   child acceptance checks beat a single-session baseline on
   coordination cost, outcome quality, or operator effort; and
2. a superseding ADR amends ADR 0006 with parent/child lineage, fan-out
   and depth, budget reservation, cancel, recovery, and result
   collection for that workload.

**D4. Constraints any adopted design must meet.** These are recorded so
a later ADR starts from them, not so this one implements them:

- **New environment from a snapshot.** The child does not share the
  parent's writable tree or live processes
  ([ADR 0035](0035-live-environment-fork-deferred.md)).
- **Bounded fan-out and depth.** Both have a small hard ceiling.
  Unbounded spawning is a non-goal of #121.
- **Budget on the project.** A reservation, if any, deducts from
  `max_concurrent_leases` (or a named successor key) at parent start
  and releases on child terminal state. Children cannot raise the
  parent's grant.
- **Overlay may only narrow.** A child cannot request a capability the
  parent session's current policy overlay does not already allow.
- **Parent cancel fails live children.** Pause and project removal do
  the same. A child's failure does not cancel the parent unless the
  superseding ADR says so.
- **Results are receipts.** The parent collects durable child
  outcomes through the plane store. It does not read the child's
  environment disk.
- **Grants rebind.** The child's per-turn grant, terminal lease, and
  ref prefix name the child session and environment. The parent grant
  does not work there.
- **No cross-project inheritance.** A child belongs to the parent's
  project.

No implementation follow-up is authorized. This ADR does not change
`policy.yaml`, the store schema, admit, or ADR 0006.

## Consequences

Easier: one session, one environment, one grant, one lease meter. The
start, snapshot, and grant contracts stay unchanged.

Harder: a task that wants parallel bounded children must be split by
the operator into independent sessions, or run as sequential turns.
There is no parent cancel of a tree, and no automatic result roll-up.

Irreversible: none. No schema, policy field, or session kind is added.

## Threat examples

The deferral removes each of these by construction. An adopting ADR
must answer each one.

- **Child widens the grant.** A child session requests egress, a
  repository, or `implement` the parent does not have. D4: overlay may
  only narrow.
- **Fan-out exhausts the host.** An unbounded spawn fills
  `max_concurrent_leases` and the single runner
  ([ADR 0029](0029-single-host-runner.md)). D4 requires a hard ceiling
  and a project reservation.
- **Parent cancel leaves children leased.** Orphan turns keep
  environments and grants. D4 fails live children with the parent.
- **Shared writable tree.** Two sessions mutate one environment. D4
  and ADR 0035 forbid it.
- **Result collected from child disk.** The parent reads another
  environment's workspace, including grant material. D4 collects
  receipts only.
- **Cross-project child.** A parent in project A starts a child bound
  to project B. D4: same project.

## Rejected alternatives

- **Select a delegation scenario now.** No decomposed-task cohort is
  named. Choosing fan-out, budget reservation, and cancel without that
  measurement would fix a contract with no evidence.
- **Reject child sessions outright.** A bounded parent-started child
  with its own environment from a snapshot is a plausible later
  audience. Deferral keeps it open behind D3.
- **Treat follow-up turns as children.** A new turn is the same
  session and environment (ADR 0006). Relabeling it would hide the
  ownership question #121 asks.
- **Infer parentage from prompt text or effort key.** Agent output
  cannot grant a child. Lineage would be a store field, not a model
  assertion.

## Validation and reversal

Validation: this ADR is Accepted in the index; `sessions` still has
`environment_id NOT NULL` and no parent column; ADR 0006 still says
one session, one environment. Reverse by a superseding ADR that meets
D3 and D4 and amends ADR 0006 in the same change.

## Sources

- [#121](https://github.com/Sannrox/rusui/issues/121)
- [#95](https://github.com/Sannrox/rusui/issues/95),
  [#103](https://github.com/Sannrox/rusui/issues/103),
  [#122](https://github.com/Sannrox/rusui/issues/122),
  [#325](https://github.com/Sannrox/rusui/issues/325)
- [ADR 0006](0006-session-start.md),
  [ADR 0007](0007-environment-snapshot.md),
  [ADR 0010](0010-hybrid-roadmap-sequence.md),
  [ADR 0035](0035-live-environment-fork-deferred.md)
- `internal/store/migrate.go` `sessions`;
  [ARCHITECTURE.md](../../ARCHITECTURE.md) `max_concurrent_leases`

# ADR 0089: The plane survives the loss of its host

- Status: Accepted
- Date: 2026-11-17
- Supersedes: [ADR 0041](0041-ha-plane-deferred.md) (an HA plane is
  deferred).
- Amends: [ADR 0001](0001-environment-plane.md) D8 (SQLite with one
  writer) and [ADR 0029](0029-single-host-runner.md) D1 (the plane and
  its runner share one host).
- Resolves: [#699](https://github.com/Sannrox/rusui/issues/699).
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0086](0086-supported-1.0-core-contract.md) (the 1.0 core excludes
  an HA plane), [#698](https://github.com/Sannrox/rusui/issues/698) and
  [its record](../proofs/698-recovery-objective.md) (objective and fence
  values), [#700](https://github.com/Sannrox/rusui/issues/700)
  (implementation), [#685](https://github.com/Sannrox/rusui/issues/685)
  (runner loss stays out of scope).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

The plane is one process on one host with one SQLite file. When that host
is lost, no session wakes, no webhook delivery is stored, no schedule fires
and no approval can be answered until an operator restores a copy. Sessions
now stay assigned for weeks and wake on events, so a plane outage is an
outage of every agent.

ADR 0041 D3 made a redundant plane a refused action until a recovery
objective was named and a superseding ADR answered its D4 constraints. The
maintainer has decided the plane must survive the loss of its host
(#699). #698 names the objective as a proposal:

- **Interruption window: 5 minutes**, from plane loss to the first
  successful wake on a surviving instance.
- **Lost acknowledged writes: none.**

Restore cannot meet the second value: everything written after the last
copy is lost. The single-host baseline drill in #698 is **not yet
measured**, so every number below is a proposal or an estimate, not a
result.

Source facts at `c2d754e`:

- `store.Open` opens SQLite with `SetMaxOpenConns(1)` and
  `_txlock=immediate` (`internal/store/store.go`). The single connection
  is the only writer fence between plane jobs.
- There is no repository interface. `Store` exposes `*sql.DB`, and
  `internal/engine` (22 files) and `internal/server` (5 files) issue SQL
  directly. The SQL uses SQLite forms such as `INSERT OR IGNORE` and
  `INSERT OR REPLACE`.
- Per-item work is already fenced: turns by `lease_generation`, with
  receipts keyed by `(job_id, lease_generation, claimed_revision)`;
  refreshes by `refresh_requests.generation` with a 2m `OwnerTTL`.
- Lease expiry compares against the plane's own clock (`e.now()`).
- External actions record `intended_actions` and `apply_attempts` before
  the call, and apply looks a stored comment up by id or marker before
  posting (#705).
- `runScheduler` (`cmd/rusui/main.go`) runs every plane-wide timer in the
  one process: refresh, reconcile, catch-up, apply retry, schedules (with
  idle sleep and retention expiry) and Sumika reconcile.
- `GET /readyz` reports `ops.Diagnose` (policy, runtime, address,
  environment). It does not test the store.
- Upgrades drain, stop, copy and swap. Two versions never run at once.
  `schema_upgrade_lock` fails an interrupted migration closed.

## Decision

**D1. Topology.** The redundant topology is two or more plane instances
with no special leader, over one shared transactional store. Every
instance serves every route. Instances do not talk to each other; the
store is their only shared state. No consensus system, service mesh or
extra runtime is added.

**D2. Store.** A second store backend, a server-class SQL database, sits
behind `internal/store`. The engine and server stop issuing SQL; the store
package owns every query and the dialect differences. The backend must
commit synchronously to storage that survives the loss of any one host
before the plane acknowledges a write; a replica that can acknowledge and
then lose a write is not supported. Store failover belongs to the
deployment. The plane retries a transaction that the store aborted, and
every retried write is idempotent by key.

**D3. Single host stays the default.** One plane instance on SQLite with
`MaxOpenConns(1)` remains supported and the default. Its loss bound stays
the copy interval, and ADR 0041 D4 "no zero-data-loss claim" still applies
to it. The redundant topology requires the shared backend; SQLite with
more than one plane process stays unsupported.

**D4. Time comes from the store.** Every lease expiry and takeover
decision in the redundant topology compares against the store's clock in
the same transaction, not an instance's clock. Clock skew between
instances then cannot extend or shorten a lease.

**D5. Per-item fences stay.** Turn and refresh leases keep their
generations. Each write by a holder checks its generation in the same
transaction. A takeover increments the generation, so the late write of a
paused or partitioned holder is refused.

**D6. Plane-wide jobs run under one fenced lease.** A `plane_job_leases`
row named `scheduler` holds `holder`, `generation` and `expires_at`. The
instance that holds it runs every `runScheduler` timer; the others serve
HTTP and try to claim it each renewal period. Proposed values from #698:
a 2m lease renewed every 40s. A claim succeeds only on an expired row and
increments the generation. Each job transaction that is not idempotent by
key rechecks `generation` in the commit and aborts on mismatch. Schedule
buckets (`sched/<id>/<bucket>`) and delivery ids stay the idempotency keys
that make a short double run harmless. On SQLite the same row is used, so
there is one code path.

**D7. Receipt identity survives takeover.** A takeover never mints a
second identity for one action. An `intended_actions` row without an
outcome is reconciled against the forge (lookup by id or marker) before
any retry, as apply does today. A turn retried after takeover gets a new
lease generation, and its receipt key differs from the old one by that
generation alone.

**D8. Rolling upgrade.** Two versions serve together for at least one
lease length plus instance start time. Migrations are expand then
contract across two releases: a release may add, never remove or rename,
anything the previous release reads. The instance that takes
`schema_upgrade_lock` migrates; the others wait. Rollback to the previous
release is supported until the contract step ships. Stored and wire
formats in [compatibility.md](../compatibility.md) stay readable by the
previous release for the window.

**D9. Readiness means able to work.** `GET /readyz` on a redundant
instance also reports a store round trip within a bound. An instance that
cannot reach the store reports not ready and takes no work. Holding the
`scheduler` lease is not a readiness condition.

**D10. Restore stays.** Copy and restore remains the last resort and the
single-host recovery path. On the shared backend, restore is the store's
own backup restore followed by `rusui restore`, which clears live leases,
drops grants and rotates the plane id as today.

**D11. Scope.** Runners stay on one host (ADR 0029 D1 for runners). A
runner reaches the plane through one address served by any instance.
Snapshots stay runner-local. The redundant topology stays excluded from
the 1.0 core (ADR 0086) until #700 delivers its acceptance evidence and a
published drill.

## Estimated result

Not measured. On plane loss:

- HTTP: surviving instances already serve. The gap is the address
  failover (load balancer or DNS health check) in front of them.
- Plane-wide jobs: taken over within one lease length (2m) after the last
  renewal. No skew margin is needed (D4).
- Turns: held by the runner, not the plane, so they continue. A heartbeat
  missed during the gap is retried within `Liveness` (3m).
- Store: if the store host is the one lost, add its failover time.

The 5-minute window holds if store failover plus address failover stays
under about 3 minutes. #700 measures both and publishes the drill.

## Split-brain threats

- **Partition between instances.** They never coordinate directly. Each
  side commits only through the store; the side that reaches the store
  holds or takes the lease, the other cannot renew and its writes fail
  the generation check.
- **Partition between an instance and the store.** It cannot commit, so
  it reports not ready (D9) and stops renewing. Its lease expires and
  another instance takes over.
- **A paused instance resumes after its lease expired.** Its next write
  carries the old generation and is refused (D5, D6). Any job it was
  running aborts.
- **Clock skew.** Expiry uses the store clock (D4). Instance clocks only
  time renewals, and a slow renewal only loses the lease.
- **Store failover.** Synchronous commit (D2) keeps every acknowledged
  write and every generation increment. In-flight transactions abort and
  retry by key. An asynchronous replica could lose a generation increment
  and let two holders commit, which is why D2 forbids it.
- **Two versions during an upgrade.** Expand then contract (D8) keeps
  both readable. The migration lock prevents two migrators.

## Consequences

Easier: losing the plane host no longer stops every session. Upgrades no
longer need a full stop on the redundant topology.

Harder:

- `internal/store` becomes the only SQL boundary. Moving the engine and
  server queries behind it is the largest part of #700.
- Two backends must pass the same store tests. The SQLite suite stays
  unchanged and keeps running.
- Lease logic moves to store time.
- Every migration from here on is expand then contract.
- Operators of the redundant topology run a database with synchronous
  replication and an address in front of the instances.

Schema: one new table, `plane_job_leases`. Policy and runner contract:
unchanged. Trust model: unchanged; the store holds the same data it holds
today. Public API: `GET /readyz` gains a store check.

Irreversible: none. The single-host default keeps working.

## Rejected alternatives

- **A standby that replays a copied file.** No fence: a resumed primary
  could commit after takeover, and writes after the last copy are lost.
- **A consensus library inside the plane.** A transactional store with
  lease rows gives the same one-holder guarantee with one fewer system.
- **A leader that serves all traffic, with followers idle.** Needs the
  same fence and adds a failover step for HTTP. Only the plane-wide jobs
  need one holder.
- **One lease per plane-wide job.** Spreads jobs across instances with no
  measured need. One `scheduler` lease is simpler; it can split later
  without a format change.
- **Replicating SQLite files between hosts.** Asynchronous by design, so
  it can lose acknowledged writes and generation increments.
- **Catch-up of schedule buckets skipped during the window.** Not
  proposed by #698 and not part of this decision.

## Validation and reversal

Validation is #700: crash, plane-wide job takeover, stale instance,
webhook during loss, uncertain external action, rolling upgrade and
rollback, readiness, restore, and the unchanged single-host suite, each a
test against the real store, plus a published drill that states the
interruption window and the bound on lost work.

If the drill misses the 5-minute window, lengthen nothing silently:
amend this ADR with the measured numbers. Reverse by a superseding ADR
that returns to ADR 0041 D1; the single-host path never depended on this
one.

## Sources

- [#699](https://github.com/Sannrox/rusui/issues/699),
  [#698](https://github.com/Sannrox/rusui/issues/698),
  [#700](https://github.com/Sannrox/rusui/issues/700),
  [#705](https://github.com/Sannrox/rusui/pull/705)
- [ADR 0001](0001-environment-plane.md) D8,
  [ADR 0029](0029-single-host-runner.md),
  [ADR 0041](0041-ha-plane-deferred.md),
  [ADR 0086](0086-supported-1.0-core-contract.md)
- [docs/proofs/698-recovery-objective.md](../proofs/698-recovery-objective.md)
- At `c2d754e`: `internal/store/store.go` `Open`,
  `internal/store/restore.go` `Restore`, `internal/store/migrate.go`
  (`lease_generation`, `intended_actions`, `schema_upgrade_lock`),
  `internal/engine/engine.go` (`OwnerTTL`, refresh claim),
  `internal/server/readyz.go`, `cmd/rusui/main.go` `runScheduler`

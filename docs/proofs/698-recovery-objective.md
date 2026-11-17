# #698 Recovery objective for a redundant plane

- Status: desk research. The baseline drill has **not yet been measured**.
- Feeds: [#699](https://github.com/Sannrox/rusui/issues/699) (the redundant
  plane ADR).
- Source baseline: `48347e0`.
- Inputs: [ADR 0041](../decisions/0041-ha-plane-deferred.md) D1 to
  D4, [upgrade.md](../upgrade.md), [operator.md](../operator.md),
  [compatibility.md](../compatibility.md), `internal/store/restore.go`,
  `internal/engine/engine.go`, `internal/engine/schedule.go`,
  `internal/engine/recover.go`, `internal/server/webhook.go`,
  `cmd/rusui/main.go` (`runScheduler`).

This record answers the decision question in #698 from code and docs.
Every value marked *measure* has to come from the drill below before
#699 cites it as a result instead of a proposal.

## What the single-host path does today

**Store.** There is one SQLite file. `store.Open` sets
`busy_timeout(5000)` and `_txlock=immediate` and limits the pool with
`SetMaxOpenConns(1)`. That single connection is the in-process writer
fence from ADR 0041 D2. No second process may write to the file.

**Restore.** `rusui restore -db <copy>` does the following:

- Keeps sessions, turns, prompts, receipts, approval records, policy
  revisions, budgets and retention class.
- Requeues every `leased` turn with its lease cleared.
- Deletes `turn_credentials` and `credential_grants`.
- Marks runtime observations unknown.
- Expires every managed guest environment, with a receipt.
- Rotates the plane id.

**Upgrade.** Drain, stop, copy `rusui.db` with its `-wal` and `-shm` files,
swap the binaries, then start. Two versions never run at the same time.
Upgrades are forward-only. An interrupted upgrade fails closed on
`schema_upgrade_lock`.

**Background loop.** `runScheduler` runs these timers:

| Tick | Period | Work |
| --- | --- | --- |
| refresh | 1s | expire refresh owners (`OwnerTTL` 2m), expire overdue leases, step refresh |
| reconcile | 5m | redeliver GitHub hook deliveries (needs `RUSUI_GITHUB_HOOK_IDS`) |
| catch-up | 15m | list open items per policy repo and refresh tracked items |
| apply | 1m | retry apply attempts |
| schedules | 1m | step schedules, sleep idle guests, expire retained sessions |
| Sumika reconcile | 1m | reconcile Sumika state |

**Turn leases.** The runner heartbeat runs every 1m (`HeartbeatEvery`).
A lease lives for 3m without a heartbeat (`Liveness`), and the execution
deadline is 12m (45m for a pinned implement turn). Receipts are keyed by
`(job_id, lease_generation, claimed_revision)`, so a stale runner's late
result never overwrites a newer generation's.

## Missed events

| Source | During an outage | After restart | Recoverable by |
| --- | --- | --- | --- |
| GitHub hook | Delivery fails at the sender. | Reconcile redelivers it within 5m, for deliveries up to 24h old (`EventMaxAge`). With no hook ids set, reconcile is skipped. Catch-up then finds open or changed items within 15m. | The plane, with no operator action, if hook ids are configured. |
| Session webhook (`/hooks/sessions/{id}`) | The sender gets a connection error. Nothing is queued. | Nothing is redelivered. A retry from the sender is safe because the delivery id (`X-Rusui-Delivery`, or a hash of the body) makes ingest idempotent. | The sender only. |
| Slack command | The sender gets an error. | Not redelivered. | The person, by resending. |
| Schedule fire | Not fired. | `StepSchedules` fires only the **current** period bucket (`now / every`), so a missed bucket is skipped, not caught up. The idempotency key `sched/<id>/<bucket>` is durable, so a restart within the same bucket never fires twice. | Nobody. A skipped fire is lost by design. |
| Leased turn | The runner keeps working and its heartbeats fail. | After a restart, the lease is requeued once `Liveness` passes. After a restore, it is requeued at once. Generation fencing discards a late result. | The plane. The work is redone, not lost. |
| Credential grant | — | Dropped by restore. Reissued when the requeued turn is claimed. | The plane. |
| Guest environment | — | Expired by restore. A new guest is provisioned on the next turn. Guests from the old plane id need manual cleanup (`operator.md`). | The plane, plus the operator for leftovers. |

**Acknowledged work.** A request that returned success has committed its
rows. With restore, everything written after the last copy is lost:
sessions, turns, receipts and approval decisions. No mechanism today
bounds that gap. ADR 0041 D4 forbids a zero-data-loss claim until it is
measured.

## Proposed objective

These are proposals for #699, not measurements.

- **Interruption window: 5 minutes.** Measured from plane loss to the
  first successful wake on the surviving plane. It matches the reconcile
  period, so a GitHub delivery missed in the window is redelivered within
  one more period. It also has room for the takeover budget in Fence: a
  2m plane lease plus skew, store failover, and the first wake. Turn
  leases are held by runners, not the plane, so a plane loss does not end
  them on a shared store. Generation fencing, not this window, stops a
  turn from being applied twice.
- **Lost acknowledged work: none.** Applies to writes that returned
  success: sessions, turns, receipts, approval decisions and budgets. A
  shared store has to commit them durably before acknowledging, so a copy
  interval cannot meet this.
- **Not covered by the objective:**
  - Session webhook and Slack deliveries that arrive while no plane is
    reachable. They stay the sender's to retry, and ingest is already
    idempotent.
  - Schedule fires skipped during the window. Any bucket that is current
    once the window ends still fires. To recover skipped fires, #699 has
    to add catch-up deliberately; this record does not propose it.
  - Live leases, grants and guests. They are rebuilt the way restore
    rebuilds them today.

## Store

What the shared transactional store has to provide follows from the
objective. #699 chooses the product.

- Synchronous durable commit before the plane acknowledges a write, to
  meet "no lost acknowledged work".
- Serializable or row-locked transactions for job claim, lease renewal and
  idempotency lookup. Today these rely on the single-writer SQLite file.
- Store failover short enough that store failover, plane takeover and
  the first wake together fit in 5 minutes. Takeover waits one lease
  length plus a skew margin (see Fence). With a 2m lease, that leaves
  store failover at most 5m − (2m + skew + first-wake time), unless the two
  run in parallel. *Measure:* the store's failover time on the target
  deployment.
- No extra runtime or consensus system beyond the store itself.

**Single-host default.** The single-host default keeps SQLite with
`MaxOpenConns(1)`, restore as the recovery path, and the copy interval as
its loss bound. ADR 0041 D4 still applies to it.

## Fence

Plane-wide jobs are the timers listed above: refresh, reconcile,
catch-up, apply, schedules and Sumika reconcile. With more than one plane,
each needs one owner at a time or an idempotent body. Proposed starting
values reuse ratios the code already runs:

| Value | Proposal | Derived from |
| --- | --- | --- |
| Lease length | 2m | `OwnerTTL` |
| Renewal period | 40s | The 3:1 ratio of `Liveness` to `HeartbeatEvery` |
| Fence token | Increasing generation, checked on every write the owner makes | `lease_generation` on turns and receipts |
| Takeover | When the lease is unrenewed past its length, plus a skew margin | *Measure:* clock skew between plane hosts |

A pause longer than the lease, such as a GC stop or a VM freeze, must
fail the generation check rather than produce a second writer. The drill
records the longest observed pause and the clock skew. If either is
larger than one renewal period, lengthen the lease before #699 adopts it.

Schedule buckets are already idempotent through `sched/<id>/<bucket>`.
GitHub reconcile and catch-up already deduplicate by delivery and item.
Both can tolerate a short double run, so in their case the fence protects
work rather than correctness.

## Upgrade window

The current upgrade has no overlap: drain, stop, then swap. A redundant
plane supports rolling deploys, so two versions run side by side for at
least one lease length (2m), plus the time to start the new instance.
*Measure:* that startup time. Running two versions at once means:

- Schema changes become expand-then-contract across two releases. The
  forward-only check stays, but a migration may not remove or rename
  anything the previous release still reads.
- The wire and stored formats in [compatibility.md](../compatibility.md)
  stay readable by the previous release during the window.

## Baseline drill (not yet measured)

This procedure becomes the restore runbook once it has been run and
timed. Record each timestamp with UTC seconds.

1. While both hosts are up, record the clock offset of A and of B from
   the shared time source (the offset the time daemon reports). Then
   start, on host A, a plane with:
   - one schedule every 1m;
   - one session with a webhook source;
   - one GitHub hook id configured;
   - one turn leased by a runner.
2. Take a consistent online copy of A's database (SQLite backup API,
   for example `sqlite3 rusui.db ".backup copy.db"`) and move it to host
   B. A raw file copy of `rusui.db` with its `-wal` and `-shm` files is
   only consistent while the plane is stopped. Record when the copy was
   taken. Copy age at loss is T0 minus that time.
3. Create one more session on A, then stop A without draining it. **T0**
   is the moment A stops.
4. During the outage:
   - send one session webhook;
   - trigger one GitHub event;
   - let at least one schedule bucket pass.
5. On B, run `rusui restore -db <copy>` and save the report: leases
   cleared, grants dropped, environments released.
6. Start the plane on B with the same policy and the same environment,
   including `RUSUI_GITHUB_HOOK_IDS`, then run `rusui resume`.
   Point the runner, the GitHub hook URL (or the DNS name or proxy in front
   of it), and the session webhook sender at B. Record when each one
   switched, because reconcile redelivers to the hook's configured URL.
7. Record **T1**, the first successful wake on B: the first claim B
   grants, at the time the lease is issued. Record the first heartbeat and
   the end of that turn separately, because heartbeat cadence and run
   length are not recovery time.
8. Record what was lost:
   - the session created after the copy;
   - whether the session webhook arrived;
   - whether the GitHub delivery was redelivered, and when;
   - which schedule buckets were skipped, from the last bucket A fired
     and the first bucket B fired;
   - whether the leased turn was requeued and finished.
9. Pauses, on a separate test plane rather than the restored one:
   - **Observed pause.** Run the plane on the target host class for at
     least a day, under normal load and through one host maintenance
     event if the platform has them. Record the largest gap between
     consecutive 1s refresh ticks.
   - **Fence check.** Today the runner holds the turn lease. Freeze the
     runner with `SIGSTOP` for longer than `Liveness` (3m). Wait until a
     second runner claims the requeued turn at a newer generation, then
     resume the first runner and confirm that its late result fails the
     generation check. This
     confirms the fence fails safe. It does not measure a pause.

| Measure | Result |
| --- | --- |
| T1 − T0 | *not yet measured* |
| Copy age at loss | *not yet measured* |
| Acknowledged writes lost | *not yet measured* (expected: everything after the copy) |
| GitHub delivery redelivered after | *not yet measured* (expected: within 5m when hook ids are set) |
| Session webhook | *not yet measured* (expected: lost unless the sender retries) |
| Schedule buckets skipped | *not yet measured* (expected: the buckets after A's last fired bucket and before B's first fired bucket) |
| Longest pause, clock skew | *not yet measured* |

## Answer for #699

- The objective is a 5-minute interruption window with no loss of
  acknowledged writes. Inbound pushes and skipped schedule buckets during
  the window are outside it.
- Restore cannot meet that objective. It remains the single-host path and
  the fallback.
- Fence starting values: a 2m lease, renewed every 40s, with a generation
  token. Adjust them after the measured pause and clock skew.
- Rolling deploys need expand-then-contract migrations and a two-version
  window of at least one lease length.
- Still open: the drill numbers, the store's own failover time, and
  whether schedules should catch up on missed buckets.

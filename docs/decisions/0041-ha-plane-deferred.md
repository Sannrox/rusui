# ADR 0041: An HA plane is deferred

- Status: Accepted
- Date: 2026-09-29
- Amends: none. [ADR 0001](0001-environment-plane.md) D8 stands: SQLite
  with one writer, behind a repository interface.
- Resolves: [#129](https://github.com/Sannrox/rusui/issues/129)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0001](0001-environment-plane.md) D8,
  [ADR 0029](0029-single-host-runner.md) (one host, one runner),
  [#94](https://github.com/Sannrox/rusui/issues/94) (restore path),
  [#118](https://github.com/Sannrox/rusui/issues/118) (operator journey),
  [#130](https://github.com/Sannrox/rusui/issues/130) (implementation of
  an HA topology stays unauthorized).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[#129](https://github.com/Sannrox/rusui/issues/129) asked whether
recovery objectives require a redundant plane or an alternate
persistence backend. Its observable outcome is a reviewed comparison of
restore-based recovery with a bounded HA topology, then adopt, narrow,
or defer. Its adoption gate requires the maintainer to select the track
after restore-based recovery demonstrably misses a selected service
objective. No RPO or RTO is named, and no restore miss is recorded.

The objects this choice binds:

| Object | Owner | Properties |
| --- | --- | --- |
| ControlPlane | operator host process | admits work; the only durable store |
| Plane store | SQLite file on that host | one writer; `SetMaxOpenConns(1)` |
| Snapshot | runner-local cache | not copied between hosts ([ADR 0029](0029-single-host-runner.md)) |
| Restore artifact | operator copy of `rusui.db` (and WAL/SHM) | `store.Restore` inventories and strips live authority |
| JobLease / PerTurnGrant | plane | live; not durable across restore |

Links: the plane has one store file; a Session, Turn, Receipt, and
Approval record live in that file; a lease and a grant do not survive
restore.

Source evidence at the commit this decision was made against:

- [ADR 0001](0001-environment-plane.md) D8: SQLite with WAL and one
  writer through the first year, behind a repository interface so
  Postgres and a leader-lease HA plane can be added later without
  touching the engine.
- `store.Open` uses `busy_timeout(5000)` and `SetMaxOpenConns(1)`
  (`internal/store/store.go`). There is one SQL connection.
- `store.Restore` opens a captured file, inventories sessions and
  turns, clears live leases, drops grants, and keeps receipts and
  approval **records** (`internal/store/restore.go`). Missing or
  corrupt files fail; they must not look like a successful restore.
- `TestCopiedDatabaseRestoresSession` copies the file and reads the
  session back (`internal/store/backup_test.go`).
- [docs/upgrade.md](../upgrade.md) drain, copy `rusui.db` (and
  `-wal`/`-shm`), replace binaries, restore on start failure.
- [ADR 0029](0029-single-host-runner.md): one runner on this host.
  Snapshot bytes are not moved. Runner loss is lease expiry plus grant
  TTL.

No measured restore or drain recovery is recorded against a chosen RPO
or RTO. Single-writer SQLite on one host is the supported recovery
option. A redundant topology has no demonstrated shortfall to close.

## Decision

**D1. Defer.** rusui does not add a second plane writer, a leader
lease, Postgres, or an active-active store in the 1.0 core. Recovery is
copy the SQLite artifact, restore it, and re-admit work. Live leases
and grants are not recovered; receipts and approval records are.

**D2. One writer stays the fence.** Partition or a second process
opening the same file is not a supported topology. `MaxOpenConns(1)` is
the in-process fence, not a distributed lock.

**D3. Adoption is a refused action until both gates hold:**

1. the maintainer names an RPO/RTO and shows a restore or drain
   measurement that misses it on the supported single-plane path; and
2. a superseding ADR amends ADR 0001 D8 with one redundant topology,
   including fencing, receipt identity across failover, and rollback.

**D4. Constraints any adopted design must meet:**

- **One writer at a time.** Leader loss or partition must not allow
  two processes to commit. Uncertain GitHub or Slack actions stay
  reconciled against the action ledger, not replayed blindly.
- **Receipts remain authoritative.** Failover must not mint a second
  identity for the same action.
- **Restore remains a path.** HA does not delete the copy-and-restore
  runbook.
- **Snapshots stay runner-local** unless a superseding ADR to ADR 0029
  moves them.
- **No zero-data-loss claim** without measured evidence.

No implementation follow-up is authorized. This ADR does not change the
store schema, `Open`, or `Restore`.

## Consequences

Easier: one SQLite file, one writer, a documented restore path.

Harder: plane-host disk or process loss is recovered from the last
copied artifact, with live leases and grants gone. There is no
automatic failover.

Irreversible: none.

## Threat examples

The deferral removes each of these by construction. An adopting ADR
must answer each one.

- **Two writers after partition.** Split brain double-admits the same
  item. D4 requires one writer and fencing.
- **Failover replays an uncertain GitHub write.** A comment or
  publication is posted twice. D4 reconciles the action ledger.
- **Restore reported success on a corrupt file.** Operators trust empty
  state. `Restore` already fails closed on missing or corrupt
  artifacts.
- **HA claimed without a measured miss.** Operating cost rises with no
  recovery benefit. D3 forbids it.

## Rejected alternatives

- **Adopt a bounded HA topology now.** No RPO/RTO is named and restore
  has no recorded miss. Choosing leader election and a second backend
  without that measurement would fix a contract with no evidence.
- **Reject HA outright.** ADR 0001 D8 already reserved a repository
  interface for later. Deferral keeps that door.
- **Replace SQLite for parity with another engine.** A non-goal of
  #129. The issue asks whether recovery objectives require HA, not
  whether another SQL dialect is nicer.
- **Active-active without a requirement.** Two writers. Rejected by D2.

## Validation and reversal

Validation: this ADR is Accepted in the index; `store.Open` still sets
`MaxOpenConns(1)`; `store.Restore` still strips live authority. Reverse
by a superseding ADR that meets D3 and D4 and amends ADR 0001 D8 in the
same change.

## Sources

- [#129](https://github.com/Sannrox/rusui/issues/129)
- [#94](https://github.com/Sannrox/rusui/issues/94),
  [#103](https://github.com/Sannrox/rusui/issues/103),
  [#118](https://github.com/Sannrox/rusui/issues/118),
  [#130](https://github.com/Sannrox/rusui/issues/130)
- [ADR 0001](0001-environment-plane.md) D8,
  [ADR 0029](0029-single-host-runner.md)
- `internal/store/store.go` `Open`;
  `internal/store/restore.go` `Restore`;
  `internal/store/backup_test.go` `TestCopiedDatabaseRestoresSession`;
  [docs/upgrade.md](../upgrade.md)

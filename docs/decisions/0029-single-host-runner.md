# ADR 0029: The supported runner topology remains one host

- Status: Accepted
- Date: 2026-09-27
- Amends: [ADR 0007](0007-environment-snapshot.md) (snapshot cache is
  runner-local; P1 is one runner) and
  [ADR 0011](0011-unattended-session-contract.md) (first unattended
  proof uses one runner).
- Resolves: [#123](https://github.com/Sannrox/rusui/issues/123)
- Amended by: [ADR 0034](0034-operator-host-location-deferred.md) (a
  registered operator host is not a session location either).
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0001](0001-environment-plane.md) D3,
  [ADR 0028](0028-container-isolation-profile.md) (container isolation
  on that one host),
  [#94](https://github.com/Sannrox/rusui/issues/94) (restore path),
  [#95](https://github.com/Sannrox/rusui/issues/95) (M1 workflow),
  [#124](https://github.com/Sannrox/rusui/issues/124) (fleet placement
  stays unauthorized).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

The snapshot artifact lives in a runner-local cache keyed by
`source_hash`. The plane stores the hash string, not the bytes.
[ADR 0007](0007-environment-snapshot.md) said P1 is one runner.
[ADR 0011](0011-unattended-session-contract.md) used one runner for the
first unattended proof. The operator guide already names one
`rusui-runner` on the same host as the plane.

[#123](https://github.com/Sannrox/rusui/issues/123) asked whether
placement, drain, and recovery across additional hosts is justified.
The adoption gate required measured queueing, resource pressure, or
recovery limits on the supported single-runner profile. No such
measurement is recorded. Moving a snapshot between hosts would change
the restore contract without a named capacity problem.

The objects this choice binds:

| Object | Owner | Properties |
| --- | --- | --- |
| ControlPlane | operator host process | admits work; the only durable store |
| Runner | same operator host | outbound-only; name `local` in `runners`; opens no inbound port |
| Snapshot | runner-local cache | prepared tree; not copied between hosts |
| Environment | plane row; container on this host | sleep/wake/destroy on this runner |
| JobLease | plane | one turn claimed by this runner |

Links: the plane has one Runner; a Session uses one Environment on that
host; an Environment is created from a Snapshot that exists only here.

## Decision

**D1. Retain one host.** The supported topology is one `rusui-runner` on
the same host as the plane. Schema seeds `runners` with one row named
`local`. `TouchRunner` updates `last_seen_at` for that name. It does
not insert a second runner.

**D2. Snapshot and environment stay on this host.** Restore after
expiry is destroy-container then re-materialize from the local
snapshot ([ADR 0007](0007-environment-snapshot.md)). Runner loss is
lease expiry plus grant TTL ([ADR 0011](0011-unattended-session-contract.md)).
There is no cross-host artifact move and no live process migration.

**D3. Adopting a fleet is a refused action.** Placement, drain, and
recovery across additional runners are unauthorized. The action is
allowed only after:

1. measured queueing, resource, or recovery limits on this single-host
   profile are recorded; and
2. a superseding ADR selects one bounded topology, ownership, and
   rollback.

A deferred or rejected outcome does not authorize
[#124](https://github.com/Sannrox/rusui/issues/124). Kubernetes, an HA
plane, and transparent live migration stay out of this profile.

**D4. Hello is presence, not minting.** `POST /runners/hello` records
that the local runner is ready. A second name does not create a second
supported host.

No implementation follow-up is authorized.

## Consequences

Easier: one restore contract, one snapshot cache, one fail-closed
startup. Operators do not run a runner fleet for 1.0.

Harder: a second physical host is not a supported placement. Snapshot
bytes are not a portable environment. Capacity is this machine.

Irreversible: none. No schema or policy field is added.

## Rejected alternatives

- **Adopt a multi-runner topology now.** No measured single-host limit
  exists. ADR 0010 already allowed fleet to remain absent from core 1.0.
- **Move snapshot bytes with the session.** That would make the plane
  store artifacts it currently hashes only.
- **Leave #123 open until a limit appears.** The research outcome is
  retain/defer. Reopen with a superseding ADR when a limit is named.
- **Kubernetes by default.** Out of the issue non-goals and this
  product's one-host contract.

## Validation and reversal

Validation: a fresh store has one `runners` row named `local`;
`TouchRunner` on another name does not insert a row; this ADR is
Accepted in the index. Reverse by a superseding ADR that selects a
fleet topology and rewrites the contract in the same change.

## Sources

- [#123](https://github.com/Sannrox/rusui/issues/123)
- [#94](https://github.com/Sannrox/rusui/issues/94),
  [#95](https://github.com/Sannrox/rusui/issues/95)
- [ADR 0007](0007-environment-snapshot.md),
  [ADR 0011](0011-unattended-session-contract.md),
  [ADR 0028](0028-container-isolation-profile.md)
- `internal/store/model.go` `LocalRunnerName`,
  `internal/store/store.go` `TouchRunner`,
  `internal/server/server.go` `runnerHello`

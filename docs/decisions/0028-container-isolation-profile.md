# ADR 0028: The supported machine isolation profile remains the container

- Status: Accepted
- Date: 2026-09-27
- Amends: [ADR 0008](0008-p1-isolation-split.md) (machine isolation is a
  container), [ADR 0007](0007-environment-snapshot.md) (snapshot is the
  prepared tree, not a hypervisor memory image), and
  [ADR 0022](0022-public-repo-isolation.md) (public-repository default).
- Resolves: [#125](https://github.com/Sannrox/rusui/issues/125)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0001](0001-environment-plane.md) D4,
  [ADR 0027](0027-guest-reachability-ask.md) (the image does not grant
  reachability),
  [#91](https://github.com/Sannrox/rusui/issues/91) (delivered container
  and credential boundary),
  [#95](https://github.com/Sannrox/rusui/issues/95) (M1 session workflow
  with live container identity),
  [#126](https://github.com/Sannrox/rusui/issues/126) (implementation of
  a second runtime stays unauthorized).
  [ADR 0029](0029-single-host-runner.md) keeps that container on one host.
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

ADR 0008 assigned machine isolation to a container and called the process
driver a test/dev stand-in. ADR 0022 made that the public-repository
unattended default and failed closed when Docker or Podman is missing.
ADR 0007 defined a **snapshot** as the prepared, reusable tree for a
`source_hash`, cached on one runner. Sleep is a stopped container; wake
is start of that same handle.

[#125](https://github.com/Sannrox/rusui/issues/125) asked whether a
stronger isolation or snapshot runtime (a second machine backend with
its own restore contract) is justified. The adoption gate required a
named isolation requirement or a measured wake/idle cost that containers
cannot meet. No such gap is recorded. Container stop/start already
covers the M1 sleep/wake path. A hypervisor snapshot target inherited
from earlier sequencing is not a current operator need.

The objects this choice binds:

| Object | Owner | Properties |
| --- | --- | --- |
| Environment | plane | one session, one environment; driver `container` or `process` |
| IsolationProfile | plane | the machine fence of that environment; supported value `container` |
| Snapshot | runner-local cache; plane stores the hash | prepared tree for `source_hash`; not a memory-plus-disk image |
| NetworkGrant | policy; overlay may only narrow | trusted egress: plane proxy hostname only |
| PerTurnGrant | plane broker | the only credential in a P1 guest outside implement |
| Session | plane | unattended kinds keep the container fence |

Links: a Session uses one Environment; an Environment is created from
one Snapshot; an Environment uses one IsolationProfile; an Environment
holds at most one PerTurnGrant.

## Decision

**D1. Retain.** The supported IsolationProfile for unattended sessions
is **container**. `Container.Kind()` is `container`. Create applies
`ApplyTrustedNetwork`. The guest may dial only `rusui.plane`
(`GuestDialAllowed`). Sleep is `Stop`; wake is `Start`. Destroy removes
the container. No second Driver kind is registered.

**D2. Snapshot stays a prepared tree.** [ADR 0007](0007-environment-snapshot.md)
stands. A snapshot is the reusable workspace tree keyed by `source_hash`.
It is not a hypervisor memory snapshot and is not restored across hosts.
P1 remains one runner. Expire destroys the container; the next turn
re-materializes from the snapshot.

**D3. Process and local stay out of this profile.** The process driver
is explicit test/dev. Sumika local sessions are a human-driven
experimental kind ([ADR 0016](0016-local-interactive-runtime.md)).
Neither is an IsolationProfile for public-repository unattended work.

**D4. Adopting a stronger profile is a refused action.** Create,
sleep, wake, and destroy against a hypervisor, microVM, or other
second backend are unauthorized. The action is allowed only after:

1. a named isolation requirement or measured wake/idle cost that the
   container path cannot meet is recorded; and
2. a superseding ADR selects one bounded backend, its restore contract,
   and an operator rollback.

A deferred or rejected outcome does not authorize
[#126](https://github.com/Sannrox/rusui/issues/126).

**D5. The grant does not move.** Policy remains the grant
([ADR 0027](0027-guest-reachability-ask.md)). A stronger runtime would
still intersect any later image ask with that grant. This ADR does not
change `policy.yaml`.

No implementation follow-up is authorized.

## Consequences

Easier: one supported machine fence, one snapshot noun, one fail-closed
startup. Operators do not provision KVM or a second runtime for 1.0.

Harder: wake latency stays container stop/start. Cross-host restore of
a live environment is out of scope. Dirt in an expired container is
gone; only the snapshot tree is reusable.

Irreversible: none. No schema or policy field is added.

## Rejected alternatives

- **Adopt a microVM or hypervisor snapshot now.** No named isolation
  gap or measured cost exists. ADR 0010 already allowed this track to
  remain absent from core 1.0.
- **Treat ADR 0007 snapshot as a memory image.** That would change the
  restore contract and the runner-local cache without a measured need.
- **Leave #125 open until a gap appears.** The research outcome is
  retain/defer. The optional track can be reopened by a superseding ADR
  when a gap is named.
- **Require a stronger runtime before public-repository sessions.**
  Contradicts ADR 0022. Container plus plane proxies meets the named
  threats.

## Validation and reversal

Validation: `Container.Kind()` is `container`; `Create` applies the
trusted network; `GuestDialAllowed` refuses off-plane hosts; `rusui
diagnose` is not ready without a container CLI; this ADR is Accepted in
the index. Reverse by a superseding ADR that selects a second backend
and rewrites the contract in the same change.

## Sources

- [#125](https://github.com/Sannrox/rusui/issues/125)
- [#91](https://github.com/Sannrox/rusui/issues/91) (closed; container
  and credential boundary)
- [#95](https://github.com/Sannrox/rusui/issues/95) (closed; M1 proof)
- [ADR 0007](0007-environment-snapshot.md),
  [ADR 0008](0008-p1-isolation-split.md),
  [ADR 0010](0010-hybrid-roadmap-sequence.md),
  [ADR 0022](0022-public-repo-isolation.md),
  [ADR 0027](0027-guest-reachability-ask.md)
- `internal/env/driver.go` `KindContainer`,
  `internal/env/container.go` `CreateSpec`,
  `internal/env/network.go` `GuestDialAllowed`,
  `internal/ops/diagnose.go` `checkRuntime`

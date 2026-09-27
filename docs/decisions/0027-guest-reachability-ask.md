# ADR 0027: The guest image does not declare reachability yet

- Status: Accepted
- Date: 2026-09-27
- Amends: [ADR 0008](0008-p1-isolation-split.md) (machine isolation),
  [ADR 0009](0009-credential-broker.md) (the grant), and
  [ADR 0022](0022-public-repo-isolation.md) (public-repository egress).
- Resolves: [#303](https://github.com/Sannrox/rusui/issues/303)
- Related: [#125](https://github.com/Sannrox/rusui/issues/125) is
  resolved by [ADR 0028](0028-container-isolation-profile.md) (retain
  container). [#126](https://github.com/Sannrox/rusui/issues/126) stays
  unauthorized. [#91](https://github.com/Sannrox/rusui/issues/91)
  delivered the container and credential boundary.
- Discussion: none. Merging with this status is the acceptance act.

## Context

Public-repository unattended sessions run in a container. Trusted egress,
the per-turn grant, and GitHub REST on the plane are already the grant:
`policy.yaml`, `ApplyTrustedNetwork`, and `GuestDialAllowed`. The guest
image carries tools and the bits named by `source_hash`. It does not
declare which hosts, credentials, or volumes it asks to reach. A
Dockerfile change can widen that need without a separate reviewable ask.

An OCI image annotation can carry a typed ask. One experimental
descriptor format does that, with the host granting, refusing, or
prompting. It is an ask format. It is not this product's session model,
policy language, or container driver. The format is not final.

## Decision

### D1. Policy remains the grant

`policy.yaml` is the grant. Egress class `trusted` means the guest may
dial only the plane proxy host. The credential broker issues the per-turn
grant. The image does not grant itself network, credentials, or volumes.

Create, when a descriptor exists later, intersects the ask with that
grant. The effective reachability is the intersection. An overlay may
only narrow it. An image ask cannot widen egress or credentials.

### D2. No descriptor until the format is final

Do not pin the experimental descriptor. Do not parse one at create.
Today's implicit ask is the whole ask:

- network: the plane proxy host only (`ApplyTrustedNetwork`,
  `GuestDialAllowed`);
- volume: the session workspace;
- credential: the per-turn grant, swapped on the plane.

A missing descriptor keeps that implicit ask. Public-repository
unattended sessions do not fail closed merely because the image has no
descriptor. They already run under the grant above.

### D3. A wider ask is a refusal

When a descriptor is enforced later, an ask wider than the grant refuses
the session. Unattended work does not prompt. There is no operator at
the keyboard, and a prompt must not stall into a wider grant.

A runtime that implements only some capability types is conforming only
when it refuses a required type it does not implement.

### D4. Types

A later descriptor may claim only:

- network limited to the plane proxy host;
- one workspace volume;
- a host-brokered credential that is the per-turn grant.

The plane refuses host network, arbitrary hosts, raw provider keys,
GitHub tokens baked into the image, extra host volumes, devices, and
privilege. Plane-specific asks (turn grant, session ref, model proxy)
use a rusui type namespace. Well-known types are used only when their
meaning is the same thing.

### D5. Identity

`source_hash` already covers the image bytes. A tag that moves to new
bytes changes that hash. This decision does not add a stored permission
surface. That store is deferred until create-time intersection exists.

No implementation follow-up is authorized until the descriptor format
is final. This ADR does not change `policy.yaml`.

## Consequences

Operators keep the current trusted-egress guest. A future image that
wants more than the plane proxy, the workspace, and the per-turn grant
cannot get it by annotation alone. The grant stays in policy, and a
wider ask is a refusal.

## Limitations

The experimental descriptor is not parsed, published, or tested here.
Stronger isolation runtimes are a separate choice. A moving image tag
is still caught by `source_hash`, not by a separate permission row.

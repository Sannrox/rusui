# ADR 0091: Enforce egress classes none, trusted and custom

- Status: Accepted
- Date: 2026-11-17
- Resolves: [#681](https://github.com/Sannrox/rusui/issues/681)
- Amends: [ADR 0001](0001-environment-plane.md) D5 (egress classes are
  enforced by the driver) and [ADR 0050](0050-guest-toolchain-in-image.md)
  D1 (adopts its rejected option 1, for operator-listed destinations only).
- Related: [ADR 0047](0047-guest-link.md), [#751](https://github.com/Sannrox/rusui/issues/751)

## Context

`policy.yaml` accepted `egress: none`, `trusted`, `custom` and `full`, and
ADR 0001 D5 says the driver enforces them. Nothing read the value. Every
container guest got the trusted path: no network, and a guest link that
forwards the plane. Guests no longer hold a GitHub credential (#607), so
no turn forwards GitHub any more. A project could not name an outside
destination, and `full` promised something no driver did.

Hosted coding agents commonly offer three settings: no network for the
agent, a trusted default, and an operator-named destination allowlist.

## Decision

**D1. Classes.** `none` and `trusted` forward only the plane. `custom`
forwards the plane plus each destination in the project's `egress_allow`.
`full` is a policy load error until #751 decides it.

**D2. Destinations are names, forwarded by the link.** An `egress_allow`
entry is `host` or `host:port` (port 443 when omitted). The guest keeps
`--network none`. Each turn, the guest link listens on a guest loopback
address for each distinct host, writes that name into the guest's hosts
file, and dials the named host from the plane side. No route, bridge, or
DNS resolver is added.

**D3. Fail closed at load.** An entry that is an IP literal, a wildcard,
`localhost` or a `.localhost` name, a single label, the plane's guest name,
a duplicate, or a port outside 1 to 65535 is a load error naming the
project. So is `egress_allow` under any class other than `custom`, and
`custom` with an empty list. A rejected reload keeps the running policy.

**D4. Per turn.** The plane sends the destinations on each turn's
assignment, so a policy change applies at the next turn without
recreating the environment.

## Consequences

- Policy contract: new project key `egress_allow`. `full` moves from
  accepted-and-ignored to a load error.
- Trust: each listed host is an operator grant to every guest of the
  project. The link fixes the destination, not the bytes: the guest picks
  what it says to that host.
- Rolling deploys: a plane on the older version rejects a file that uses
  `egress_allow` (unknown key) and keeps its running policy. An older
  runner ignores the field on the assignment and forwards the plane only.
  Either way the guest gets less than asked, never more.
- Setup hooks and image builds are unchanged.

## Rejected alternatives

- **A CONNECT proxy or a filtered bridge.** ADR 0047 refused both; a
  name-per-target link keeps the container with no network.
- **Wildcard or address-range destinations.** Each entry should be one
  reviewable grant.
- **Writing the names at environment create.** A policy change would wait
  for a new environment, and a reordered list could point one name at
  another's target.

## Validation and reversal

`TestEgressAllow` covers load errors. `TestLiveCustomGuestReachesListedDestinationOnly`
proves a live guest reaches a listed name and not an unlisted name, port,
or address. To reverse, drop `egress_allow` and make `custom` a load
error; guests fall back to the plane only.

## Sources

- [#681](https://github.com/Sannrox/rusui/issues/681), ADR 0001, 0047, 0050.

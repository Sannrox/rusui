# ADR 0093: Egress class full forwards HTTP(S) through a link proxy

- Status: Accepted
- Date: 2026-11-18
- Resolves: [#751](https://github.com/Sannrox/rusui/issues/751)
- Amends: [ADR 0047](0047-guest-link.md) (rejected alternative "an HTTP
  CONNECT egress proxy", for class `full` only) and
  [ADR 0091](0091-egress-classes.md) D1 (`full` is no longer a load error).
- Related: [ADR 0050](0050-guest-toolchain-in-image.md)
- Discussion: none. Merging with this status is the acceptance act.

## Context

Some projects need outbound access the operator cannot list in advance,
for example installing packages that are not in the image. ADR 0091 made
`egress: full` a load error until it meant something. Two facts constrain
the design:

- ADR 0047 D1 gives every managed guest `--network none`. That keeps the
  plane, the host's own services, and private networks out of reach by
  construction, and a bridge would need per-runtime, per-host proof to
  restrict again.
- An unrestricted route still must not reach the plane host or private
  ranges. A check on the name alone is not enough: a public name can
  resolve, or later rebind, to a private address.

ADR 0047 rejected a CONNECT proxy because a guest on a bridge could
ignore it. A guest with no network that ignores the proxy has no route.

## Decision

**D1. No network, one more link target.** A `full` guest keeps
`--network none`. Its guest link adds one target, `127.0.0.5:3128` in the
guest, and the runner serves it with a per-turn HTTP and CONNECT forward
proxy on host loopback. The guest gets `HTTPS_PROXY` and `HTTP_PROXY` for
that address, and `NO_PROXY` for `rusui.plane`, `localhost`, and
`127.0.0.1`.

**D2. The proxy checks the dialed address.** The proxy resolves each
name itself. At connect time, for every address it tries, it refuses
anything that is not public unicast: loopback, private, link-local
(cloud metadata included), multicast, unspecified, shared address space,
benchmarking, reserved and protocol ranges, IPv6 prefixes that embed an
IPv4 address (6to4, NAT64, Teredo, mapped), and every address assigned to
the runner's host, which is the plane host under ADR 0029. A refused
destination gets `403`.

**D3. HTTP(S) only.** CONNECT is accepted for port 443 only, and absolute
`http://` requests for any port. SSH, mail, UDP, and other raw TCP have
no route. Clients that ignore proxy settings fail. A tunnel carries
whatever the guest sends on 443; the proxy does not inspect TLS.

**D4. The class travels on the assignment.** The plane sends
`egress: full` on each turn's assignment, so a policy change applies at
the next turn. Process guests are a non-goal and get no proxy.

## Consequences

- Policy contract: `full` loads. Every other class is unchanged.
- Trust: `full` is an operator grant of the public internet to every
  guest of the project. The proxy fixes where bytes may go, not what they
  say. It does not log destinations.
- The per-turn proxy listens on host loopback for the turn, so a local
  host process could use it too. It grants such a process no more than
  the host's own network already does, minus the refused ranges.
- The turn's receipt names the policy revision it was claimed under
  (ADR 0090), and `rusui policy explain` prints the project's class. No
  receipt field is added.
- Rolling deploys: an older plane rejects `full` and keeps its running
  policy. An older runner ignores `egress` and forwards the plane only.
  Either way the guest gets less than asked, never more.
- A host whose public address is assigned behind NAT cannot list it, so
  the proxy cannot refuse it by address. Reaching the host's public
  address from outside is already possible for any internet client.

## Rejected alternatives

- **A container network for `full`.** Opens every protocol, needs a
  per-runtime filter to keep the host and private ranges out, and loses
  the property that a test can observe the guest has no network.
- **A name allowlist with a wildcard.** That is `custom`; `full` exists
  for destinations the operator cannot name.
- **Check the name, then dial.** A name can rebind between the check and
  the dial; the check belongs on the dialed address.

## Validation and reversal

`TestEgressAddrAllowed` covers each refused range.
`TestEgressProxyRefusesHostServicesAndNonHTTPSTunnels` proves a host service is refused by
address and by name over CONNECT and HTTP, and that a tunnel to a port
other than 443 is refused.
`TestLiveFullGuestReachesPublicThroughProxyOnly` proves a live guest
reaches a public host through the proxy, and not host loopback, a name
for it, the bridge gateway, cloud metadata, or any address directly.
`TestLiveTrustedGuestReachesOnlyLinkTargets` is unchanged.

To reverse, make `full` a load error again. The proxy holds no state.

## Sources

- [#751](https://github.com/Sannrox/rusui/issues/751), ADR 0047, 0090,
  0091.

# ADR 0047: Container guests reach the plane only through a stdio guest link

- Status: Accepted
- Date: 2026-10-02
- Amends: [ADR 0022](0022-public-repo-isolation.md) (public-repository
  egress), [ADR 0027](0027-guest-reachability-ask.md) D1 and D2 (how the
  implicit network ask is enforced), and
  [ADR 0028](0028-container-isolation-profile.md) D1 (`ApplyTrustedNetwork`).
- Resolves: [#435](https://github.com/Sannrox/rusui/issues/435)
- Related: [ADR 0015](0015-agent-publication.md) (implement sessions talk
  to GitHub), [ADR 0029](0029-single-host-runner.md) (one host),
  [ADR 0044](0044-plane-publishes-from-turn-result.md) (plane
  publication), [#429 results](../proofs/429-managed-rerun-results.md).
- Discussion: none. Merging with this status changed to Accepted is the
  acceptance act.

## Context

A container guest needs four network paths, and the container driver
gives it a Docker bridge network for all of them:

1. guest to the plane model proxy and git proxy (`rusui.plane`, TLS, the
   per-turn grant);
2. plane to a guest port for preview (`PortForward` dials the container
   address on the bridge);
3. in implement sessions only, guest to GitHub with the operator
   credential ([ADR 0015](0015-agent-publication.md));
4. nothing else: egress class `trusted` grants the plane proxy host only.

The bridge does not deliver these on every supported host:

- `rusui.plane` maps to `host-gateway`. On native Linux Docker that is
  the bridge address, while the plane listens on loopback, so path 1
  never connects (#435). Docker Desktop forwards `host-gateway` to host
  loopback, which is why earlier proofs on macOS and in a VM passed.
- A host firewall may drop bridge-to-host traffic. The #429 host did, so
  binding the plane on the bridge would still need an operator firewall
  change, which an unattended host install should not require.
- Path 4 is a promise about what the network forbids. A bridge forbids
  nothing by itself; any restriction would have to be added and then
  proved per runtime and host.
- Docker Desktop documents that container bridge addresses are not
  routable from the macOS host, so path 2 dials an address the plane may
  not reach there (not verified on macOS for this ADR).

The runner and the plane already drive every guest over `docker exec`
stdio (ACP turns, workspace reads and writes, file upload). `docker exec`
behaves the same on native Linux Docker, Podman, and Docker Desktop.

## Decision

### D1. The guest has no network

The container driver creates managed guests with `--network none`.
There is no bridge, no `host-gateway` entry, and no route out of the
container. `rusui.plane`, and in implement sessions the granted GitHub
hosts, resolve to `127.0.0.1` inside the guest. Egress class `trusted`
is enforced by construction: a destination the link does not forward
does not exist for the guest.

### D2. Guest links carry every guest connection

A **guest link** is a long-lived `docker exec -i <guest> node -e
<program>` whose stdio carries a multiplexed stream protocol in both
directions. The guest side is a short Node program passed in the exec
arguments, so the image needs Node (the reference image has it; the
preview sidecar this replaces already needed it) and nothing else.

- **Out, owned by the runner per turn:** the guest side listens on fixed
  guest loopback addresses (`rusui.plane` maps to `127.0.0.2`, the
  GitHub names to `127.0.0.3` and `127.0.0.4`). Each accepted connection
  becomes a stream, and the runner dials that address's target. The
  plane target is the plane listener the runner itself uses. The target
  list is fixed when the link starts; the guest cannot name one.
- **In, owned by the plane per environment:** for preview, the plane
  opens a stream to a guest port, and the guest side dials
  `127.0.0.1:<port>` inside the guest. `PortForward` returns a
  plane-local listener bound to that link instead of a bridge address.

The link forwards bytes. TLS stays end to end: the guest still verifies
the plane certificate (`rusui.plane` SAN, plane CA mount) and still
sends only its per-turn grant. A forwarded GitHub host keeps GitHub's
own certificate and SNI.

A stream that cannot keep up is reset rather than allowed to stall the
other streams on the link. An oversized or malformed frame ends the link.

### D3. Forward targets are policy, not image

| Session | Forwarded targets |
| --- | --- |
| every managed session | `rusui.plane:<plane port>` to the plane listener on host loopback |
| implement, agent publication ([ADR 0015](0015-agent-publication.md)) | also `github.com:443` and `api.github.com:443`, dialed by name from the plane host |
| implement, plane publication ([ADR 0044](0044-plane-publishes-from-turn-result.md)) | nothing beyond the plane |

An overlay may only remove targets. The image cannot add one
([ADR 0027](0027-guest-reachability-ask.md) D1 and D3 stand).

### D4. Lifecycle follows the environment

A turn's link starts before the harness and ends with the turn. A link
that fails to start fails the turn; a link that ends mid-turn cancels
the turn at once instead of letting it wait for the execution deadline.
The preview link starts on first use and restarts on the next request
after it ends; sleep, expire, replace, and destroy close it.

### D5. Diagnose proves the path

`rusui diagnose` reports a `guest_link` check for the container
topology: start a throwaway guest from `RUSUI_GUEST_IMAGE` with
`--network none`, open a link, fetch the plane health endpoint over TLS
through it, and confirm a direct address is unreachable. The check is
not `ready` unless both hold. `/readyz` does not run it, because it
starts a container.

## Consequences

- #435 is resolved on native Linux Docker, proven live. Podman and
  Docker Desktop use the same mechanism but are not verified; see
  Evidence. The plane keeps listening on loopback; no
  operator firewall change is required.
- Trusted egress becomes a property of the container configuration that
  a test can observe, instead of a policy function no runtime applies.
- Preview stops depending on routable container addresses.
- Guest setup that downloads from the internet (`.agents/setup`
  installing packages) no longer works unless the content is in the
  snapshot or fetched through the plane git proxy. That was never
  granted by egress class `trusted`; operators who relied on it will
  see setup failures and need to bake dependencies into the image.
- The guest image needs Node. An image without it fails the turn and
  `guest_link` explicitly.
- The runner, which already execs the harness, also execs the turn's
  link; the assignment format is unchanged.
- The `rusui-trusted` network that setup creates is no longer used by
  managed guests.
- No schema or public API change. Policy gains no new field; D3 derives
  targets from existing session kind, `implement`, and
  `RUSUI_PUBLICATION`.

## Rejected alternatives

- **Plane listens on the bridge gateway; `rusui.plane` maps to it.**
  Fixes path 1 on Linux only when the host firewall allows it, adds a
  non-loopback plane listener, and leaves path 4 and macOS path 2
  unsolved.
- **An `--internal` bridge plus an egress filter.** Restricts path 4 but
  needs per-runtime, per-host proof (Docker Desktop, Podman rootless,
  nftables versus iptables) and still needs path 1 solved.
- **Bind-mount a plane Unix socket into the guest.** Clean on Linux, but
  Docker Desktop cannot share host Unix sockets with containers, so
  macOS would need a second mechanism.
- **An HTTP CONNECT egress proxy on the bridge.** Restricts destinations
  only for clients that honor proxy settings; a guest process can ignore
  it, so it is not a boundary.
- **Run the harness on the host and only tools in the guest.** Removes
  guest networking entirely, but moves model credentials and the agent
  loop out of the isolation boundary and rewrites the provider boundary
  ([ADR 0025](0025-provider-boundary.md)).

## Validation and reversal

Proof of concept on 2026-10-02 (native Linux Docker 29.7.2, reference
guest image at `fb13ce1`, a throwaway relay outside the repository):

- guest with `--network none`: `api.github.com` failed to resolve and
  `1.1.1.1` was unreachable;
- `https://rusui.plane` through the link: TLS verified against the
  mounted CA; 20 parallel 1 MiB downloads all matched the server
  checksum (199 ms);
- inbound: a host request through the link reached a server listening
  only on the guest's `127.0.0.1`;
- a real Claude Code 2.1.283 turn (`claude-sonnet-5`) inside the
  network-less guest completed through the link in 4 s.

Implementation evidence (same host): link tests run the real guest
program for both directions, 20 parallel 1 MiB streams, a refused target,
and a malformed guest; `rusui diagnose` reported `guest_link: ready` live;
a managed `run` turn and a follow-up on the same environment completed
live with the guest at `network=none`, where a direct dial failed with
`ENETUNREACH`.

The #429 matrix rerun on this design is recorded in the
[second results](../proofs/429-managed-rerun-2-results.md) (narrow; no
identity, authorization, or ownership cell failed). Accepted without a
live turn on Docker Desktop (macOS) or Podman: neither is verified, and
until one is, the supported managed profile stays native Linux Docker.

Reversal: restore `ApplyTrustedNetwork` and the bridge. The link adds no
persisted state, so reverting is a code change only.

## Sources

- [#435](https://github.com/Sannrox/rusui/issues/435)
- [#429 results](../proofs/429-managed-rerun-results.md)
- `internal/env/network.go`, `internal/env/port_forward.go`,
  `internal/runner/runner.go` at `fb13ce1`

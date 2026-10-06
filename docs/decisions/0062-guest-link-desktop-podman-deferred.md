# ADR 0062: Docker Desktop and Podman stay unverified for the guest link

- Status: Accepted
- Date: 2026-10-06
- Amends: [ADR 0047](0047-guest-link.md) (guest link proven on native
  Linux Docker; Podman and Docker Desktop unverified).
- Resolves: [#498](https://github.com/Sannrox/rusui/issues/498)
- Related: [docs/proofs/498-guest-link-hosts.md](../proofs/498-guest-link-hosts.md)
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

ADR 0047's guest link is verified on native Linux Docker. The ADR
says Podman and Docker Desktop use the same mechanism and are not
verified.

[#498](https://github.com/Sannrox/rusui/issues/498) asked for one
predeclared run of `rusui diagnose` plus a managed prompt, follow, and
preview on each host. Each host ends go, narrow, or defer. A host that
cannot pass `guest_link` is defer, not a silent skip.

## Decision

**D1. Both hosts defer on this run.** Neither Docker Desktop nor
Podman met the pass bar recorded in
[498-guest-link-hosts.md](../proofs/498-guest-link-hosts.md).

**D2. Native Linux Docker remains the verified host.** ADR 0047
stands. This ADR does not declare Docker Desktop or Podman supported.

**D3. A later go needs a new run** against a predeclared SHA that
meets the same pass bar on that host.

## Consequences

- Easier: operators still target native Linux Docker for container
  guests.
- Harder: Docker Desktop and Podman wait for a run that starts
  `guest_link` and finishes prompt, follow, and preview.
- Schema, policy, runner contract, and public API are unchanged.

## Rejected alternatives

- **Declare either host supported from the mechanism.** ADR 0047
  already said they are unverified. This run did not change that.
- **Silent skip.** The issue required an explicit defer.

## Validation and reversal

Accept on merge. Validated when the proof names the candidate SHA,
the pass bar, and a defer for each host.

Reverse with a superseding ADR that records a go or narrow from a
new predeclared run.

## Sources

- [#498](https://github.com/Sannrox/rusui/issues/498)
- [ADR 0047](0047-guest-link.md)
- [498-guest-link-hosts.md](../proofs/498-guest-link-hosts.md)
- Decided against `main` at `aaa74c1`.

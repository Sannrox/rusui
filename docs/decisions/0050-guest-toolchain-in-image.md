# ADR 0050: Bake guest toolchain into the image; add no destinations

- Status: Accepted
- Date: 2026-10-06
- Amends: [ADR 0047](0047-guest-link.md) D3 (forward targets stay the
  plane and, in implement sessions, GitHub). Related image change is
  [#500](https://github.com/Sannrox/rusui/issues/500), not this ADR.
- Resolves: [#481](https://github.com/Sannrox/rusui/issues/481)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (guest link;
  unchanged until that file is rewritten),
  [ADR 0018](0018-rusui-setup.md) (reference guest image),
  [ADR 0027](0027-guest-reachability-ask.md) (the image does not grant
  itself network), [ADR 0036](0036-environment-desktop-deferred.md)
  (a graphical desktop stays deferred).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[ADR 0047](0047-guest-link.md) creates managed guests with
`--network none`. The guest link forwards `rusui.plane` on every
managed session and, in implement sessions with agent publication,
`github.com:443` and `api.github.com:443`. An overlay may only remove
targets. The image cannot add one.

[#481](https://github.com/Sannrox/rusui/issues/481) asked which
destinations, if any, the guest link may forward beyond the plane and
GitHub, without putting a durable secret in the guest. Three options:

1. A named allowlist of destinations, forwarded by the existing guest
   link. Anything else fails closed.
2. Bake the toolchain and a headless browser into the image, and add
   no destinations.
3. Retain `--network none` with only the plane and GitHub paths, and
   change nothing about the image.

ADR 0047 already recorded that `.agents/setup` installing packages
from the internet is not granted by egress class `trusted`; operators
who relied on it bake dependencies into the image or fetch them
through the plane git proxy. [ADR 0027](0027-guest-reachability-ask.md)
keeps policy as the grant: a wider image ask is a refusal. A graphical
desktop stays deferred ([ADR 0036](0036-environment-desktop-deferred.md));
a headless browser in the image is not a desktop.

[#500](https://github.com/Sannrox/rusui/issues/500) is the image
change. Closing this research as retain with no image change does not
satisfy that dependency. An image-only decision that still names the
outcome does.

Source evidence at the commit this decision was made against:

- `PlaneLinkTargets` returns one target (the plane) or three (plane
  plus the two GitHub hosts). There is no registry, index, or apt
  host on that list.
- `GuestDialAllowed` under trusted egress allows only the plane host.
- The reference image is Node slim plus git, gh, and a pinned Claude.
  It does not contain a headless browser.

## Decision

**D1. Add no destinations.** The guest link still forwards only the
plane and, in implement sessions with agent publication, GitHub. There
is no package-registry, index, or apt allowlist. Option 1 is refused.

**D2. Bake the toolchain.** A session that must run the project's
documented tests or capture a page gets those tools from the guest
image, not from a new network path. [#500](https://github.com/Sannrox/rusui/issues/500)
is the issue that changes the image. This ADR does not implement it.

**D3. Network contract of ADR 0047 stands.** `--network none`, the
stdio guest link, and "the image cannot add a forward target" remain.
This ADR does not supersede them.

## Consequences

- Easier: one network path. Setup that needs packages is an image
  rebuild, which the reference image already is.
- Harder: a project whose tests need a tool that is not in the image
  waits for an image change rather than an egress exception.
- Schema, policy, runner contract, and public API are unchanged. Trust
  model is unchanged: no durable secret in the guest, no general
  internet route.
- `ARCHITECTURE.md` still describes the guest link destinations. It
  changes only when that file is next rewritten.

## Rejected alternatives

- **Named allowlist (option 1).** ADR 0047 already refused a CONNECT
  proxy, an internal bridge plus filter, and extra destinations the
  image could add. An allowlist reopens that boundary for every
  registry host and still needs a secret-free fetch story.
- **Retain with no image change (option 3).** Leaves "a session cannot
  install tools or capture a page" unsolved. [#500](https://github.com/Sannrox/rusui/issues/500)
  says that closing as retain with no image change does not satisfy
  its dependency.

## Validation and reversal

Accept on merge. Validated when `PlaneLinkTargets` still returns only
the plane, or the plane plus GitHub on implement, and
`GuestDialAllowed` still refuses package-registry hosts.

Reverse with a superseding ADR that names extra forward targets, or
that withdraws the image-only outcome from #500.

## Sources

- [#481](https://github.com/Sannrox/rusui/issues/481)
- [ADR 0047](0047-guest-link.md) D1, D3, and consequences
- [ADR 0027](0027-guest-reachability-ask.md)
- Decided against `main` at `d981a8c`.

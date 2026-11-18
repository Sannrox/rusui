# ADR 0094: A second isolation profile gives each environment its own guest kernel

- Status: Proposed
- Date: 2026-11-18
- Amends: [ADR 0028](0028-container-isolation-profile.md) D1 and D4
  (supersedes in part: the container is no longer the only profile),
  [ADR 0008](0008-p1-isolation-split.md) (machine isolation is a
  container or a guest-kernel machine),
  [ADR 0007](0007-environment-snapshot.md) (what sleep keeps for this
  profile), and [ADR 0022](0022-public-repo-isolation.md) (either profile
  meets the public-repository fence).
- Resolves: [#694](https://github.com/Sannrox/rusui/issues/694)
- Related: [ADR 0029](0029-single-host-runner.md) (one host),
  [ADR 0035](0035-live-environment-fork-deferred.md), [ADR 0047](0047-guest-link.md),
  [ADR 0076](0076-tenant-isolation.md), [ADR 0078](0078-session-execution-modes.md),
  [#683](https://github.com/Sannrox/rusui/issues/683),
  [#685](https://github.com/Sannrox/rusui/issues/685).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status changed to Accepted is the acceptance act, and requires the
  spike result in Validation to be attached first. The same change adds
  "amended by 0094" to the status lines of ADR 0007, 0008, 0022 and 0028;
  a proposal does not.

## Context

ADR 0028 kept the container as the only isolation profile because no
requirement was named that a container cannot meet, and its D4 named
the gate: a recorded requirement and a superseding ADR that selects one
bounded backend, its restore contract, and a rollback.

The requirement now exists. ADR 0076 and 0078 let one plane host
sessions for several tenants and for several members of a tenant, each
in user mode, running as that member. Those users do not trust each
other, and their environments share one host (ADR 0029). Every container
on the host shares one kernel: a kernel bug, or a gap in the container
profile, lets one user's guest read or change another user's
environment. The operator who hosts more than one tenant, or user-mode
sessions of more than one member, has this requirement. A single
operator running their own sessions does not.

The container profile's other boundaries do not depend on the kernel
being shared: the guest has no network and reaches the plane only over
a guest link on a stdio pair (ADR 0047), credentials stay at the plane,
and policy is the grant (ADR 0027).

## Decision

**D1. Two profiles.** IsolationProfile takes `container` or `machine`.
In `machine`, each environment is a lightweight virtual machine with its
own guest kernel, run by a virtual machine monitor on the runner host
with hardware virtualization. One environment is one machine. The
driver kind is `machine`; `container` stays the default and stays
supported.

**D2. Selected per host by the operator.** The runner's configuration
names the profile for every environment it creates. A plane that admits
more than one tenant, or user-mode sessions of more than one member,
should run `machine`; this ADR does not enforce that. Policy, leases,
grants, and receipts do not change. A runner configured for `machine`
that cannot provide it is not ready (D6); each environment row already
records the driver kind that created it, so an operator can see which
profile served a session. Changing a runner's profile, in either direction, is
one procedure: drain the runner, expire every environment of the old
profile, then restart it on the new one. A turn never wakes an
environment under a profile other than the one that created it.

**D3. Same guest boundary, different carrier.** The machine has no
network device. The guest link protocol (ADR 0047) and ACP run over a
host-to-guest socket stream that the monitor provides instead of the
container runtime's exec stdio pair. Link targets, egress classes (ADR 0091 and its
amendments), the plane CA mount, and the per-turn grant are unchanged.
Workspace reads and writes use the same stream.

**D4. One bounded backend.** The driver targets exactly one monitor,
pinned to one release and checked by digest in diagnose. That monitor
must: run on the host's hardware virtualization; boot a guest kernel
directly, without firmware; attach block devices for the root filesystem
and the writable disk; provide a host-to-guest socket stream; start a
machine with no network device; and be driven through a documented
control interface. The spike selects the monitor against this list, and
the acceptance change records the result; a second monitor needs a new
ADR. The machine boots a pinned guest kernel and a root filesystem built
from the same guest image as the container profile, so one image
definition serves both. Kernel and root filesystem are content-addressed
and verified before boot.

**D5. Sleep is a stopped machine; wake is a cold boot.** ADR 0007
stands: a snapshot is the prepared tree for a `source_hash`, never a
memory image. Sleep shuts the machine down and keeps its writable disk.
Wake boots the same kernel, root filesystem, and writable disk, and
reattaches the workspace. Running processes do not survive sleep, as
with a stopped container. Expire destroys the machine and its disk; the
next turn materializes again from the snapshot. No machine state moves
between hosts.

**D6. A missing capability fails closed.** On a runner configured for
`machine`, `rusui diagnose` reports a `machine_profile` check that is
ready only when the host exposes hardware virtualization to the runner
user, the pinned monitor, kernel, and root filesystem verify, and a
throwaway machine boots, opens a guest link, fetches the plane health
endpoint through it, and has no other route. `GET /readyz` is not ready
while the configured profile's capability check fails. The runner never
falls back to `container` on its own.

**D7. The public-repository fence holds under either profile.** ADR
0022 D1's machine isolation for unattended `visibility: public` sessions
is the runner's configured profile, `container` or `machine`. Its terms
stay as written for both: the guest sees the image and the session
workspace, egress is the project's class through the guest link, and
the guest holds only the per-turn grant. ADR 0022 D3's fail-closed
runtime check is the configured profile's check: on a `machine` runner,
public-repository work does not start unless `machine_profile` is
ready, and it never falls back to `container` or `process`.

## Consequences

- Trust: a guest kernel escape no longer reaches other users'
  environments directly; the remaining shared surface is the monitor
  and the host kernel's virtualization interface.
- Operators of the `machine` profile provision hardware virtualization
  (bare metal or nested), the monitor, and the kernel. Hosts without it
  keep `container`.
- Wake is a cold boot instead of a container start. The spike measures
  it; the profile does not promise container wake time.
- The adversarial suite (#683) is the acceptance test for the profile,
  run against `machine` as it is against `container`.
- No schema, policy, wire, or public API change. The environment row
  already stores its driver kind.
- The driver gets its own Issue once this ADR is accepted; nothing here
  authorizes it before then.

## Rejected alternatives

- **Harden the container profile instead.** A stricter syscall filter
  or a user-space kernel narrows the shared surface but keeps one host
  kernel under every user's guest, which is the requirement's threat.
- **Memory snapshots for sleep and wake.** Faster wake, but it turns
  ADR 0007's snapshot into a memory image, adds a restore contract per
  monitor version, and has no measured need.
- **Choose the profile per project in policy.** Policy would gain a
  field for what is a property of the host, and one host would have to
  run both runtimes. Per-project choice can be added later if a mixed
  host is needed.
- **One machine per user holding many environments.** Fewer boots, but
  environments of one user would share a kernel again and sleep would
  no longer be per environment.
- **Wait for a runner fleet (#685).** The requirement exists on the one
  runner now; a fleet can place environments by profile later.

## Validation and reversal

Before acceptance, a time-boxed spike, not merged, attaches to #694:
one environment boots from the guest image's root filesystem, runs a
guest over ACP through the guest link on the host-to-guest stream,
sleeps, and wakes, with the measured wake time.

After the driver lands: the #683 adversarial suite passes against
`machine`; a public-repository session on a `machine` runner with
`machine_profile` not ready does not start; `machine_profile` is not ready on a host without hardware
virtualization; and a cross-environment probe from one machine cannot
reach another's workspace.

Reversal: the D2 procedure back to `container`. Running `machine`
environments finish their turns first. Sleeping ones are expired, which
loses work on their writable disks that was not published, exactly as
an expire does under either profile, and as adopting `machine` does for
sleeping containers; the next turn materializes again from the snapshot.
Nothing in the plane's store changes.

## Sources

- [#694](https://github.com/Sannrox/rusui/issues/694),
  [#125](https://github.com/Sannrox/rusui/issues/125),
  [#126](https://github.com/Sannrox/rusui/issues/126)
- ADR 0007, 0008, 0022, 0027, 0028, 0029, 0047, 0076, 0078
- `internal/env/driver.go` (`Driver`, `KindContainer`)

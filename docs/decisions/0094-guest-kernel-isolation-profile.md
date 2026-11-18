# ADR 0094: A verified operator backend gives each environment its own guest kernel

- Status: Proposed
- Date: 2026-11-18
- Amends: [ADR 0028](0028-container-isolation-profile.md) D1 and D4
  (supersedes in part: the container is no longer the only profile, and
  the second profile is bounded by a verified contract instead of one
  named backend), [ADR 0008](0008-p1-isolation-split.md) (machine
  isolation is a container or a guest-kernel machine),
  [ADR 0007](0007-environment-snapshot.md) (what sleep keeps for this
  profile), and [ADR 0022](0022-public-repo-isolation.md) (either profile
  meets the public-repository fence).
- Resolves: [#694](https://github.com/Sannrox/rusui/issues/694)
- Related: [ADR 0029](0029-single-host-runner.md) (one host),
  [ADR 0047](0047-guest-link.md), [ADR 0076](0076-tenant-isolation.md),
  [ADR 0078](0078-session-execution-modes.md),
  [#683](https://github.com/Sannrox/rusui/issues/683),
  [#685](https://github.com/Sannrox/rusui/issues/685).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status changed to Accepted is the acceptance act, and requires the
  spike result in Validation to be attached first. The same change adds
  "amended by 0094" to the status lines of ADR 0007, 0008, 0022 and 0028;
  a proposal does not.

## Context

ADR 0028 kept the container as the only isolation profile because no
requirement was named that a container cannot meet. Its D4 gate asked
for a recorded requirement and a superseding ADR that bounds the second
backend, its restore contract, and a rollback.

The requirement now exists. ADR 0076 and 0078 let one plane host
sessions for several tenants and for several members of a tenant, each
in user mode, running as that member. Those users do not trust each
other, and their environments share one host (ADR 0029). Every container
on the host shares one kernel: a kernel bug, or a gap in the container
profile, lets one user's guest reach another user's environment. The
operator who hosts more than one tenant, or user-mode sessions of more
than one member, has this requirement. A single operator running their
own sessions does not.

Running virtual machines well (a monitor, a guest kernel, root
filesystems, fast boot) is a product of its own, and operators who need
this profile usually already run one. What rusui depends on is narrow:
the container driver uses a handful of lifecycle calls and one exec
stdio stream (`env.Runtime`, `env.StdioExecutor`), and everything else,
the guest link, ACP, workspace reads and writes, rides that stream. The
properties rusui relies on (no route out, the plane CA, the per-turn
grant only) are observable from inside the guest, which `rusui
diagnose` already checks for containers (ADR 0047 D5).

## Decision

**D1. Two profiles.** IsolationProfile takes `container` or `machine`.
`container` is built in, supported, and the default. In `machine`, each
environment runs in its own virtual machine with its own guest kernel,
provided by an operator backend (D2) that rusui verifies (D4). Driver
kind `machine` is recorded on each environment row.

**D2. The backend is a program behind a fixed contract.** The runner's
configuration names one backend program. rusui runs it once per
operation, with the operation and the environment handle:

| Operation | Input | Must |
| --- | --- | --- |
| `create` | the environment spec as JSON: name, guest image reference, CPU and memory limits, the plane CA file, the guest host names | boot a machine from the image with no network device, the CA mounted, and the host names resolved; print the handle |
| `stop` | handle | stop the machine and keep its disk |
| `start` | handle | boot the same machine and disk again |
| `remove` | handle | delete the machine and its disk |
| `exec` | handle, argv, env | run argv in the guest with stdin and stdout attached until either side closes; exit with argv's exit status |

A handle names the backend that issued it. `start`, `stop`, and `remove`
of a handle the backend did not issue exit with status 3, distinct from
other failures; `exec` of one fails, and its status is never read as a
mismatch, since it carries the guest command's own. Wake always calls
`start` before any `exec`. Every operation is
safe to repeat: `create` of an existing name returns
its handle, `stop` of a stopped machine and `remove` of a missing one
succeed. Every operation except `exec` finishes within five minutes or
fails. A non-zero exit is a failure. The backend's stderr, and for
`exec` the guest command's stderr, goes to the runner log; `exec`'s
stdout is the guest stream and never the log. The backend program is trusted operator code that runs with the
runner's privileges, as the container runtime does today. rusui ships no
backend, only the contract and the checks.

**D3. Same guest boundary, different carrier.** The guest link protocol
(ADR 0047), ACP, and workspace reads and writes run over the backend's
`exec` stream exactly as over a container's. Link targets, egress
classes (ADR 0091 and its amendments), the plane CA, and the per-turn
grant are unchanged. The backend runs its machines on the runner host
(ADR 0029) and the runner reaches them only through the contract.
Placing machines on other hosts belongs to a runner fleet (#685).

**D4. rusui verifies the properties, not the vendor.** On a runner
configured for `machine`, `rusui diagnose` reports a `machine_profile`
check. It creates two throwaway environments through the backend and is
ready only when, for each:

- a guest link opens over `exec` and fetches the plane health endpoint
  over TLS verified against the mounted CA;
- the guest has no network interface but loopback (`/sys/class/net`
  lists only `lo`), and a direct dial to a public address, the host's
  bridge or gateway address, cloud metadata, and the other environment
  all fail;
- a guest process named with a unique marker does not appear in the
  runner host's process table (`/proc/*/cmdline`), as every
  container's processes do. The marker travels over `exec`'s stdin to a
  fixed guest command that starts a process named with it, so it is on
  no host command line, the backend's included, and the check excludes
  no host process. Before trusting an absence, the check reads the
  command line of PID 1 and of a process owned by another user; if
  `/proc` hides either, it is not ready;
- the guest's kernel boot id (`/proc/sys/kernel/random/boot_id`) differs
  from the host's and from the other environment's;
- `stop`, `start`, and `remove` succeed twice in a row, and `start`
  finds a file written before `stop`.

Given machines on the runner host and a runner in the host's PID
namespace, a guest process absent from the runner's process table runs
on another kernel. The boot id is a second signal only: a
system-container manager can give each container its own. Three things
stay operator statements, recorded with the backend's digest: that the
backend places its machines on the runner host, that the runner runs in
the host's PID namespace and not inside a container, and that the
backend uses hardware virtualization rather than a user-space kernel.
Under those statements the check makes a network, a missing CA, and a
shared host kernel detectable; it does not make the statements
themselves observable.

`GET /readyz` is not ready while the check fails, and reports the
backend's path and digest. The runner never falls back to `container` on
its own.

**D5. Sleep keeps the disk; wake runs resume.** ADR 0007 stands: a
snapshot is the prepared tree for a `source_hash`, never a memory image.
Sleep is `stop`; wake is `start` and then `.agents/resume`, as for a
container. A backend may also keep memory, but rusui never relies on a
process surviving sleep. Expire is `remove`; the next turn materializes
again from the snapshot.

**D6. Selected per host.** The profile is a runner setting for every
environment it creates. A plane that admits more than one tenant, or
user-mode sessions of more than one member, should run `machine`; this
ADR does not enforce that. Policy, leases, grants, and receipts do not
change. Changing a runner's profile, in either direction, is one
procedure: drain the runner, expire every environment of the old
profile, then restart it on the new one. Pointing the runner at another
backend program is the same procedure, since handles belong to the
backend that issued them. An upgrade of the same program keeps its
handles valid or is treated as a change. The procedure is the
operator's. If it is skipped, the runner still fails closed: before any
driver call it compares the environment row's driver kind with its
configured profile, and a backend refuses another backend's handle with
status 3 (D2). On either mismatch the environment expires without
calling any driver: its row is marked expired, and the old machine or
container and its disk are left for the operator to remove with the
runtime that made it.

**D7. The public-repository fence holds under either profile.** ADR
0022 D1's machine isolation for unattended `visibility: public` sessions
is the runner's configured profile. Its terms stay as written for both:
the guest sees the image and the session workspace, egress is the
project's class through the guest link, and the guest holds only the
per-turn grant. ADR 0022 D3's fail-closed check is the configured
profile's check: on a `machine` runner, public-repository work does not
start unless `machine_profile` is ready, and it never falls back to
`container` or `process`.

## Consequences

- Trust: a guest kernel escape no longer reaches other users'
  environments directly. Machine locality, the runner's PID namespace,
  and hardware virtualization are operator statements (D4). The backend program and the monitor behind it
  join the trusted base, chosen and pinned by the operator.
- rusui builds one adapter (the contract as an `env.Runtime` and
  `env.StdioExecutor`) and one check, not a monitor integration, a guest
  kernel, or a root filesystem build.
- Operators of `machine` bring a backend that boots the guest image with
  no network device. Hosts without one keep `container`.
- Wake time and whether memory survives depend on the backend. The spike
  measures one; the profile promises neither.
- The adversarial suite (#683) is the acceptance test for the profile,
  run against `machine` as it is against `container`.
- Configuration contract: one new runner setting names the profile and
  one names the backend program. No schema, policy, wire, or public API
  change; the environment row already stores its driver kind.
- The driver gets its own Issue once this ADR is accepted; nothing here
  authorizes it before then.

## Rejected alternatives

- **One built-in monitor integration.** rusui would own a monitor
  release, a guest kernel, and a root filesystem pipeline to deliver a
  property it can check instead, and operators who already run machines
  would get a second stack.
- **Trust the backend's word.** An unverified backend turns "the guest
  has no network and does not share the host's kernel" into a promise.
  D4 keeps both detectable on the runner host, as ADR 0047 made trusted
  egress observable, and names what remains the operator's statement.
- **Harden the container profile instead.** A stricter syscall filter or
  a user-space kernel narrows the shared surface but keeps one host
  kernel under every user's guest, which is the requirement's threat.
- **Memory snapshots as the restore contract.** It turns ADR 0007's
  snapshot into a memory image and ties restore to one backend's format.
  A backend may keep memory; rusui does not depend on it.
- **Choose the profile per project in policy.** Policy would gain a field
  for what is a property of the host.
- **Wait for a runner fleet (#685).** The requirement exists on the one
  runner now; a fleet can place environments by profile later.

## Validation and reversal

Before acceptance, a time-boxed spike, not merged, attaches to #694: a
backend program for one existing virtual machine service implements D2;
two environments boot from the guest image and pass the D4 checks, and
one runs a guest over ACP through the guest link, sleeps, and wakes,
with the measured wake time.

After the driver lands: the #683 adversarial suite passes against
`machine`; `machine_profile` is not ready against a backend that returns
containers on the host's kernel (marker process visible on the host,
even with a per-container boot id) or gives the guest a network device; a probe from one `machine` environment cannot reach another's
workspace or any of its addresses;
and a public-repository session on a `machine` runner whose check fails
does not start.

Reversal: the D6 procedure back to `container`. Running `machine`
environments finish their turns first. Sleeping ones are expired, which
loses work on their disks that was not published, exactly as an expire
does under either profile, and as adopting `machine` does for sleeping
containers; the next turn materializes again from the snapshot. Nothing
in the plane's store changes.

## Sources

- [#694](https://github.com/Sannrox/rusui/issues/694),
  [#125](https://github.com/Sannrox/rusui/issues/125),
  [#126](https://github.com/Sannrox/rusui/issues/126)
- ADR 0007, 0008, 0022, 0027, 0028, 0029, 0047, 0076, 0078
- `internal/env/container.go` (`Runtime`, `StdioExecutor`),
  `internal/env/driver.go` (`Driver`, `ResumePath`)

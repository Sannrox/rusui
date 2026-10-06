# ADR 0052: Attach and the guest keep separate shells

- Status: Accepted
- Date: 2026-10-06
- Amends: [ADR 0019](0019-rusui-attach-client.md) (attach is the
  operator terminal transport; it is not the guest's shell).
- Resolves: [#483](https://github.com/Sannrox/rusui/issues/483)
- Related: [ADR 0012](0012-operator-access.md) (one terminal write
  lease), [ARCHITECTURE.md](../../ARCHITECTURE.md) (unchanged until
  rewritten), [#502](https://github.com/Sannrox/rusui/issues/502)
  (one shared terminal session remains blocked).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

`rusui attach` leases a terminal. The guest's shell and the operator's
attach are not one session, so the guest cannot read a long-running
command the operator started.

[#483](https://github.com/Sannrox/rusui/issues/483) asked whether the
environment should run one terminal session that attach joins and the
guest can read. Two options:

1. One session inside the environment. Attach joins it. The guest can
   read its output. A write still takes the terminal lease.
2. Retain separate attach and guest shells.

[ADR 0019](0019-rusui-attach-client.md) made `rusui attach` the
runtime-owned terminal transport. [ADR 0012](0012-operator-access.md)
gives the operator one exclusive write lease. The guest talks ACP over
stdio on a different exec. Merging those into one session is a new
multiplexed object: join, read-without-write, and what the guest sees
when the operator detaches. No named task requires the guest to read
an operator-started command.

Source evidence at the commit this decision was made against:

- A run session starts with no terminal lease.
- Attach acquires an exclusive write lease; a second writer is
  refused; a worker token cannot open the operator terminal.
- Guest exec for attach input is a separate `ExecStdio` from the
  harness stdio.

## Decision

**D1. Retain separate shells.** The operator attach PTY and the
guest's shell are different sessions. Attach does not join a guest
shell. The guest does not read attach output.

**D2. The write lease stays exclusive.** A write still takes the
terminal lease ([ADR 0012](0012-operator-access.md)). This ADR does
not add a second write lease.

**D3. One shared terminal stays refused** until a superseding ADR
names one task that must continue from an operator-started command
inside the guest. [#502](https://github.com/Sannrox/rusui/issues/502)
remains blocked on that later choice.

## Consequences

- Easier: attach and the harness keep the transports they already
  have. No shared PTY, no join protocol, no guest-visible attach
  buffer.
- Harder: the guest cannot inspect a command the operator typed in
  attach.
- Schema, policy, runner contract, trust model, and public API are
  unchanged.

## Rejected alternatives

- **One session inside the environment (option 1).** Adds a shared
  PTY and a read path for the guest without a named task that needs
  it. The exclusive write lease would still exist, so the new object
  is complexity on top of the current lease.

## Validation and reversal

Accept on merge. Validated when a run session holds no terminal lease
until attach, and a second writer is still refused while the lease is
held.

Reverse with a superseding ADR that names the shared session, how
attach joins it, and how the guest reads it.

## Sources

- [#483](https://github.com/Sannrox/rusui/issues/483)
- [ADR 0019](0019-rusui-attach-client.md), [ADR 0012](0012-operator-access.md)
- Decided against `main` at `9f61a4a`.

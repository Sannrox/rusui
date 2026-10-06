# ADR 0065: One terminal session inside the environment

- Status: Accepted
- Date: 2026-10-06
- Amends: [ADR 0019](0019-rusui-attach-client.md). Attach joins the
  environment terminal. It is still the runtime-owned transport.
- Supersedes: [ADR 0052](0052-separate-attach-and-guest-shells.md)
- Resolves: [#483](https://github.com/Sannrox/rusui/issues/483)
  option 1
- Related: [ADR 0012](0012-operator-access.md) (one write lease),
  [ARCHITECTURE.md](../../ARCHITECTURE.md) (unchanged until rewritten),
  [#502](https://github.com/Sannrox/rusui/issues/502) (implementation)
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[#483](https://github.com/Sannrox/rusui/issues/483) asked whether the
environment runs one terminal session that attach joins and the guest
can read. [ADR 0052](0052-separate-attach-and-guest-shells.md) chose
option 2: attach PTY and the guest shell stay different sessions.

The maintainer directed that rusui match a researched
operator-visible remote-environment product unless a rusui contract is
a total conflict. In that product the operator terminal is the same
filesystem as the agent, so a command the operator starts is visible
to the agent and a command the agent leaves running is visible to the
operator.

That product does not totally conflict with rusui. [ADR 0012](0012-operator-access.md)
still requires one writer. ACP stdio remains the guest protocol; this
ADR does not merge that transport with attach. It merges the shell
inside the environment.

[#502](https://github.com/Sannrox/rusui/issues/502) is the
implementation. This ADR does not change attach code.

## Decision

**D1. One terminal session.** The environment has one shell session.
Attach joins it. The guest can read its output, including a command
the operator started and left running.

**D2. The write lease stays exclusive.** A write still takes the
terminal lease ([ADR 0012](0012-operator-access.md)). A second writer
is refused while the lease is held.

**D3. The transcript is unchanged.** The raw terminal byte stream is
not persisted as the transcript.

## Consequences

- Easier: the operator and the guest share one working shell. A long
  command does not disappear when attach detaches.
- Harder: attach output is no longer private from the guest. Operators
  who typed a secret into that shell have shown it to the guest.
- Worker and attach change in #502. Policy, guest link, and
  publication are unchanged.
- `ARCHITECTURE.md` changes only when that file is next rewritten.

## Rejected alternatives

- **Separate shells (option 2 / ADR 0052).** Conflicts with the
  directed product shape. Not a total rusui-contract conflict that
  would keep it.

- **A second write lease.** Refused by the issue non-goals and by
  ADR 0012.

## Validation and reversal

Accept on merge. Validated when #502 lands: the operator starts a
command, detaches, and the guest reads new output from that same
session; a second writer is refused.

Reverse with a superseding ADR that restores separate shells.

## Sources

- [#483](https://github.com/Sannrox/rusui/issues/483)
- [ADR 0052](0052-separate-attach-and-guest-shells.md)
- [ADR 0012](0012-operator-access.md)
- [ADR 0019](0019-rusui-attach-client.md)
- Decided against `main` at `3003b18`.

# ADR 0064: Session size and a wait when the lease budget is full

- Status: Accepted
- Date: 2026-10-06
- Amends: [ADR 0029](0029-single-host-runner.md) D3 still refuses a
  fleet. Queueing on this single host is accepted here and is not a
  fleet.
- Supersedes: [ADR 0051](0051-fail-on-full-lease-budget-retained.md)
- Resolves: [#482](https://github.com/Sannrox/rusui/issues/482)
  option 1
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (`max_concurrent_leases`
  still caps running leases; unchanged until that file is rewritten),
  [#501](https://github.com/Sannrox/rusui/issues/501) (size and queue
  implementation)
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[#482](https://github.com/Sannrox/rusui/issues/482) asked whether a
session carries a size the container runtime enforces, and whether
admission queues with a visible wait instead of failing.
[ADR 0051](0051-fail-on-full-lease-budget-retained.md) chose option 3:
fail-on-full and one unbounded container.

The maintainer directed that rusui match a researched
operator-visible remote-environment product unless a rusui contract is
a total conflict. In that product each machine has a size, and when
the burst of running machines is full the next start waits instead of
failing.

That product does not totally conflict with rusui. [ADR 0029](0029-single-host-runner.md)
keeps one host; a queue on that host is the wait the operator already
meets when the lease budget is full. A second host stays refused. Size
is container CPU and memory, not a hypervisor.

[#501](https://github.com/Sannrox/rusui/issues/501) is the
implementation. This ADR does not add schema.

## Decision

**D1. A session has a size.** The container runtime enforces CPU and
memory for that size. A project may name a default. A session may
override it. Zero or omitted still means the runtime default until
#501 names the concrete sizes.

**D2. Admission queues when the lease budget is full.** When leased
turns on a project already meet `max_concurrent_leases`, the next
session waits. `rusui sessions` shows the wait. It starts when a lease
frees. The operator can cancel it while it waits; cancel creates no
environment.

**D3. Fail-on-full is withdrawn.** `ErrBudget` is not the response to
a full lease budget for a session that is willing to wait.

**D4. One host stands.** This is not a fleet and not a second runner.
[ADR 0029](0029-single-host-runner.md) D1 and D3 stand.

## Consequences

- Easier: a second session on a busy project waits instead of failing.
  Large work can ask for more CPU and memory than a small edit.
- Harder: the operator sees a wait. One-host memory is still finite;
  a large size can starve the host. Queue fairness is FIFO per
  project until #501 records otherwise.
- Schema and admit change in #501. Guest link, publication, and Slack
  are unchanged.
- `ARCHITECTURE.md` still describes fail-on-full. It changes only when
  that file is next rewritten.

## Rejected alternatives

- **Fail-on-full (option 3 / ADR 0051).** Conflicts with the directed
  product shape. Not a total rusui-contract conflict that would keep
  it.

- **Queue only, no size (option 2).** The directed product sizes the
  machine. Option 1 includes both.

- **A second host when the queue is long.** [ADR 0029](0029-single-host-runner.md)
  still refuses a fleet.

## Validation and reversal

Accept on merge. Validated when #501 lands: two sizes produce
different container limits; a session past the budget is queued then
runs without a 409; cancel removes it from the queue.

Reverse with a superseding ADR that restores fail-on-full or that
drops size.

## Sources

- [#482](https://github.com/Sannrox/rusui/issues/482)
- [ADR 0051](0051-fail-on-full-lease-budget-retained.md)
- [ADR 0029](0029-single-host-runner.md)
- Decided against `main` at `1226b15`.

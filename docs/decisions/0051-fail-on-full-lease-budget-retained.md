# ADR 0051: Admission still fails when the lease budget is full

- Status: Superseded by [ADR 0064](0064-session-size-and-admission-queue.md)
- Date: 2026-10-06
- Amends: none. [ADR 0029](0029-single-host-runner.md) D3 still
  requires measured queueing or resource pressure before a fleet.
- Resolves: [#482](https://github.com/Sannrox/rusui/issues/482)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (project
  `budgets.max_concurrent_leases`, default 1; unchanged until that
  file is rewritten), [#501](https://github.com/Sannrox/rusui/issues/501)
  (size and queue remain blocked on a later reopen).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

Every environment is one container on one host
([ADR 0029](0029-single-host-runner.md)). Project
`budgets.max_concurrent_leases` defaults to 1. A claim over that cap
returns `ErrBudget`. The request fails; nothing waits.

[#482](https://github.com/Sannrox/rusui/issues/482) asked whether a
session should carry a size the container runtime enforces, and
whether admission should queue with a visible wait instead of
failing. Three options:

1. A size on the session, enforced as container limits, and a queue
   when the lease budget is full.
2. A queue only. No size.
3. Retain fail-on-full and one unbounded container.

The container driver already accepts optional `CPUMillis` and
`MemoryBytes` on a create spec and passes `--cpus` / `--memory` only
when those fields are greater than zero. That is a create-time limit,
not a session size object. No measured queueing, resource, or recovery
limit on the single-host profile is recorded
([ADR 0029](0029-single-host-runner.md) D3). A queue would add a wait
state to admit. A required session size would duplicate the optional
spec fields.

Source evidence at the commit this decision was made against:

- `Project.MaxConcurrentLeases()` defaults to 1.
- Claim at the cap returns `ErrBudget` and no claim.
- `CreateEnvironment` stores `CPUMillis` and `MemoryBytes` as 0 when
  the spec omits them; the Docker CLI then omits `--cpus` and
  `--memory`.

## Decision

**D1. Retain fail-on-full.** When leased turns on a project already
meet `max_concurrent_leases`, the next claim fails with `ErrBudget`.
Admission does not queue.

**D2. No session size object.** A session does not carry a required
size. Optional container CPU and memory on the environment spec stay
optional. Zero means the runtime default: one unbounded container on
this host.

**D3. Queue and required size stay refused** until a superseding ADR
records measured pressure on this single-host profile and names the
wait the operator would see. [#501](https://github.com/Sannrox/rusui/issues/501)
remains blocked on that later choice.

## Consequences

- Easier: one admit outcome (lease or fail). No wait object, no size
  schema, no new policy field.
- Harder: an operator at the cap retries or raises
  `max_concurrent_leases`. A noisy neighbor on the host is not fenced
  by a required size.
- Schema, policy, runner contract, trust model, and public API are
  unchanged.
- `ARCHITECTURE.md` still states fail-on-full. It changes only when
  that file is next rewritten.

## Rejected alternatives

- **Session size and a queue (option 1).** Adds a size object and a
  wait state without a measured full-budget case. Optional create
  limits already exist.
- **Queue only (option 2).** Replaces a clear fail with a wait the
  operator cannot yet see, still without measured pressure.

## Validation and reversal

Accept on merge. Validated when a second claim at the default cap of
one returns `ErrBudget`, and a create with no CPU or memory omits
runtime limit flags.

Reverse with a superseding ADR that names the wait, the size unit, and
the measured pressure that made fail-on-full the wrong default.

## Sources

- [#482](https://github.com/Sannrox/rusui/issues/482)
- [ADR 0029](0029-single-host-runner.md) D3
- Decided against `main` at `a97621e`.

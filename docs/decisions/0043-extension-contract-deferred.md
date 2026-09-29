# ADR 0043: An extension contract is deferred

- Status: Accepted
- Date: 2026-09-29
- Amends: none. [ADR 0003](0003-operator-surface.md) stands: HTTP+SSE,
  CLI, ACP facade, console, Slack notify/approve.
- Resolves: [#133](https://github.com/Sannrox/rusui/issues/133)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0024](0024-session-surface.md),
  [ADR 0031](0031-cli-command-tree.md),
  [#134](https://github.com/Sannrox/rusui/issues/134) (implementation of
  an extension contract stays unauthorized).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[#133](https://github.com/Sannrox/rusui/issues/133) asked whether
integrations need a frozen plugin or extension contract. Freezing one
before concrete uses would create compatibility and security
obligations. Its observable outcome is keep the existing surfaces, or
propose a minimal contract grounded in at least three real
integrations. Its adoption gate requires those three integrations to
show an unmet common need. None are named.

The objects this choice binds:

| Object | Owner | How an integrator reaches it |
| --- | --- | --- |
| Session / Turn / Receipt | plane store | HTTP+SSE and the `rusui` CLI |
| ACP session | plane facade | editors and onmyoji as ACP clients |
| Slack | adapter | notify and approve; HMAC + user allowlist |
| Policy | plane | `policy.yaml`; overlay may only narrow |

There is no plugin host, in-process extension, or third-party webhook
out of the guest.

Source evidence at the commit this decision was made against:

- [ADR 0003](0003-operator-surface.md): every surface reads the same
  objects. New operator features land on HTTP first. Slack is not a
  transcript channel.
- [ADR 0024](0024-session-surface.md): unspecified clients fail closed
  until an accepted ADR names them.
- The HTTP API, ACP facade, and Slack hook are the shipped integration
  surfaces (`internal/server`, `internal/acp`, `internal/slack`).
- No inventory of three real integrations that require a core change
  exists in the issue or in dogfood.

## Decision

**D1. Defer.** rusui does not add a plugin boundary, extension SDK, or
in-process third-party loader in the 1.0 core. Integrators use the
object API, the CLI, ACP, or Slack notify/approve.

**D2. Unspecified clients fail closed.** A new client kind needs an
accepted ADR that names it ([ADR 0024](0024-session-surface.md)).

**D3. Adoption is a refused action until both gates hold:**

1. the maintainer names at least three real integrations whose needs
   otherwise require core changes, and why HTTP, CLI, ACP, or Slack do
   not suffice; and
2. a superseding ADR specifies the smallest shared contract, isolation,
   versioning, and operator disable/upgrade.

**D4. Constraints any adopted design must meet:**

- **Not a marketplace.** No arbitrary in-process execution.
- **Error isolation.** An extension failure does not take the plane
  writer down.
- **Operator disable.** Policy or config can turn it off; overlay may
  only narrow.
- **No workflow language.** A non-goal of #133.
- **Capability and data boundaries** explicit: which objects it may
  read, and that it cannot mint grants.

No implementation follow-up is authorized.

## Consequences

Easier: one API, one ACP facade, one Slack adapter. No ecosystem
promise.

Harder: an integration that cannot be expressed as HTTP, CLI, ACP, or
Slack stays out of tree until D3.

Irreversible: none.

## Threat examples

The deferral removes each of these by construction. An adopting ADR
must answer each one.

- **In-process loader runs guest-equivalent code on the plane.**
  Secrets and the SQLite writer are in-process. D4 forbids arbitrary
  in-process execution.
- **Extension mints a grant.** Agent or plugin output becomes
  authority. D4: it cannot mint grants.
- **Frozen SDK with no callers.** Compatibility burden with no
  users. D3 requires three real integrations first.
- **Extension outage blocks admit.** D4: error isolation.

## Rejected alternatives

- **Propose a minimal contract now.** No three integrations are named.
- **Reject extensions outright.** Three later callers with a common
  gap are a plausible audience. Deferral keeps that door.
- **Let ACP or Slack grow into a plugin host.** They are already
  bounded adapters. Widening them would hide a new trust boundary.

## Validation and reversal

Validation: this ADR is Accepted in the index; no plugin loader is
registered; ADR 0024 still fails unspecified clients closed. Reverse
by a superseding ADR that meets D3 and D4.

## Sources

- [#133](https://github.com/Sannrox/rusui/issues/133)
- [#103](https://github.com/Sannrox/rusui/issues/103),
  [#118](https://github.com/Sannrox/rusui/issues/118),
  [#134](https://github.com/Sannrox/rusui/issues/134)
- [ADR 0003](0003-operator-surface.md),
  [ADR 0024](0024-session-surface.md)
- `internal/server`, `internal/acp`, `internal/slack`

# ADR 0083: Retain single-operator access for shared sessions

- Status: Accepted on merge
- Date: 2026-10-20
- Amends: [ADR 0039](0039-shared-operator-governance-deferred.md)
  (restates the D3 gate and fixes the form a later actor must take).
- Resolves: [#578](https://github.com/Sannrox/rusui/issues/578)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0012](0012-operator-access.md) (one operator),
  [ADR 0059](0059-unattributed-operator-retained.md) (a member header
  is not identity),
  [ADR 0073](0073-session-plane-guest-registry.md) (the session is the
  product), [ADR 0078](0078-session-execution-modes.md) (`act` on the
  session), [#128](https://github.com/Sannrox/rusui/issues/128)
  (implementation track, stays blocked).
- Discussion: none. Merging with this status is the acceptance act.
  Until merge this is a proposal, not implementation authority.

## Context

ADR 0073 lets any authenticated client attach to a session. #578 asked
whether more than one person should open, follow, and continue the same
session, and through which identity. ADR 0039 D3 refuses a second
operator until the maintainer names the actor, their project scope, and
the need the single operator cannot meet.

No deployment has named that need. Tenants already separate who sees
what (ADR 0076, ADR 0077), and ADR 0078 records `act` on each session
from the presented credential. The people who approve work today are
already named by the upstream system that issues the operator's
credential and keeps its own audit. What rusui cannot do alone is
revoke one person, or name the approving person on a rusui receipt.
Nobody has asked for either.

## Decision

**D1. Retain single-operator access.** rusui adds no shared-operator
credential class, no user table, and no per-person approval in this
release. ADR 0039 D1, D2, and D4 stand unchanged.

**D2. Restated gate.** A person-level actor is added only when a named
deployment needs rusui itself to revoke one person, or to name the
approving person on a rusui receipt, and the upstream audit cannot meet
that need. This replaces the "need the single token cannot meet" clause
of ADR 0039 D3 gate 1. Gate 2 (a superseding ADR) still applies.

**D3. Assertion-only path.** If the gate opens, the person arrives as a
verified upstream identity assertion carrying `sub` (the person) and
`act` (the delegating actor), per the shared identity contract ADR 0078
already uses. It does not arrive as a rusui user table, a second static
token, or a caller-supplied header (ADR 0059 D2). The ADR 0039 D4
constraints apply to that design: per-actor revocation, fail closed when
the issuer is unavailable, and the single-operator local path keeps
working without a hosted identity provider.

## Consequences

- Easier: one operator class, no new schema, no new credential store.
- Harder: two people who want to continue the same session share the
  operator's access, and rusui receipts do not tell them apart.
- A later design has one shape to propose (verified assertion), so it
  cannot drift into a local account system.
- Schema, policy, runner contract, trust model, and public API are
  unchanged.

## Rejected alternatives

- **Add a second operator token now.** No named need. A static token per
  person would make rusui an account store, with rotation and revocation
  that the upstream system already does.
- **A rusui user table with roles.** Same problem, more surface.
- **Leave ADR 0039 D3 as written.** "A need the single token cannot
  meet" invites arguments about convenience. The restated gate names the
  two capabilities only rusui can provide.

## Validation and reversal

Validated while no rusui store, policy, or handler change adds a
person-level credential without a superseding ADR. #128 stays blocked.

Reverse with a superseding ADR that names the deployment, the need under
D2, and the assertion issuer, revocation, and receipt column under D3.

## Sources

- [#578](https://github.com/Sannrox/rusui/issues/578),
  [#128](https://github.com/Sannrox/rusui/issues/128)
- [ADR 0012](0012-operator-access.md),
  [ADR 0039](0039-shared-operator-governance-deferred.md),
  [ADR 0059](0059-unattributed-operator-retained.md),
  [ADR 0078](0078-session-execution-modes.md)
- Decided against `main` at `495b7f4`.

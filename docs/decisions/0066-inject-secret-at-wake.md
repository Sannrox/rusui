# ADR 0066: Inject a short-lived secret at wake

- Status: Accepted
- Date: 2026-10-06
- Amends: [ADR 0009](0009-credential-broker.md). Durable secrets stay
  on the plane. The guest may hold injected values only while awake.
- Supersedes: [ADR 0053](0053-wake-secret-injection-refused.md)
- Resolves: [#484](https://github.com/Sannrox/rusui/issues/484)
  option 1
- Related: [ADR 0033](0033-third-party-identity-deferred.md) (OIDC and
  plane-minted third-party identity stay deferred; this ADR is
  injection of stored secret ids),
  [ARCHITECTURE.md](../../ARCHITECTURE.md) (unchanged until rewritten),
  [#503](https://github.com/Sannrox/rusui/issues/503) (implementation)
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[#484](https://github.com/Sannrox/rusui/issues/484) asked whether the
plane may inject a value at wake, remove it on sleep, and record a
receipt that names the secret id and not the value.
[ADR 0053](0053-wake-secret-injection-refused.md) chose option 2: the
guest holds only the per-turn model and git grant.

The maintainer directed that rusui match a researched
operator-visible remote-environment product unless a rusui contract is
a total conflict. In that product project and personal values are
present in the machine at start and after wake, refresh reloads them,
and long-lived keys do not live on disk as the preferred path.

That product does not totally conflict with rusui. [ADR 0009](0009-credential-broker.md)
already keeps durable secrets on the plane and grants per turn. Inject
at wake is the same broker, with a value that dies with sleep. A
long-lived token in the image stays a non-goal. OIDC stays
[ADR 0033](0033-third-party-identity-deferred.md); this issue listed
it as a non-goal.

[#503](https://github.com/Sannrox/rusui/issues/503) is the
implementation. This ADR does not add schema.

## Decision

**D1. Inject allowed secret ids at wake.** The plane injects only the
secret ids the project allows. The guest may use the value during the
wake. A secret id the project may not use is refused before the turn
starts.

**D2. Remove on sleep.** After sleep the value is gone from the
environment. A later inspection does not contain it.

**D3. Receipt the id.** The receipt names the secret id, the session,
and the turn. The value is absent from the receipt, `rusui read`, hook
logs, and transcripts.

**D4. Durable secrets stay on the plane.** ADR 0009 stands for
storage. A long-lived token in the image stays a non-goal.

## Consequences

- Easier: a turn that must present a third-party credential has a
  value that dies with the wake.
- Harder: every wake and sleep must install and strip the values.
  Refresh of a running environment is #503's problem if a value
  rotates mid-wake.
- Credential broker and environment lifecycle change in #503. Guest
  link destinations and publication are unchanged.
- `ARCHITECTURE.md` changes only when that file is next rewritten.

## Rejected alternatives

- **Refuse injection (option 2 / ADR 0053).** Conflicts with the
  directed product shape. Not a total rusui-contract conflict that
  would keep it.

- **A long-lived token in the image.** Issue non-goal. Breaks ADR 0009.

- **OIDC in this ADR.** Issue non-goal. ADR 0033 stands until a
  superseding ADR on #322.

## Validation and reversal

Accept on merge. Validated when #503 lands: during the turn the guest
can use the value; after sleep the environment does not contain it; a
disallowed secret id is refused before the turn starts.

Reverse with a superseding ADR that restores the grant-only guest.

## Sources

- [#484](https://github.com/Sannrox/rusui/issues/484)
- [ADR 0053](0053-wake-secret-injection-refused.md)
- [ADR 0009](0009-credential-broker.md)
- Decided against `main` at `5f0e726`.

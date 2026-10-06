# ADR 0053: The guest still holds only the per-turn grant

- Status: Accepted
- Date: 2026-10-06
- Amends: [ADR 0009](0009-credential-broker.md) (durable secrets stay
  on the plane; the guest holds only the per-turn grant).
- Resolves: [#484](https://github.com/Sannrox/rusui/issues/484)
- Related: [ADR 0033](0033-third-party-identity-deferred.md) (OIDC and
  third-party identity stay deferred),
  [ARCHITECTURE.md](../../ARCHITECTURE.md) (unchanged until rewritten),
  [#503](https://github.com/Sannrox/rusui/issues/503) (inject-at-wake
  remains blocked).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[ADR 0009](0009-credential-broker.md) keeps durable secrets on the
plane. The guest holds only the per-turn grant: that value is
`XAI_API_KEY` / `ANTHROPIC_AUTH_TOKEN` and git HTTP auth, not the
xAI or GitHub secret. A turn that must present a third-party
credential has no value that dies with the wake.

[#484](https://github.com/Sannrox/rusui/issues/484) asked whether the
plane may inject a value at wake, remove it on sleep, and record a
receipt that names the secret id and not the value. Two options:

1. Inject only secret ids the project allows, remove them on sleep,
   receipt the id.
2. Retain the current refusal. The guest holds only the per-turn
   model and git grant.

Option 1 is a new broker path: allowed ids, inject, remove on sleep,
receipts that must never print the value. [ADR 0033](0033-third-party-identity-deferred.md)
already deferred plane-minted third-party identity. No named turn
requires a third-party credential that the git and model proxies
cannot present.

Source evidence at the commit this decision was made against:

- `DriverEnv` sets the turn token as the model key and, when present,
  the git proxy. It does not inject a project secret id.
- Wake starts the container and refreshes `expires_at`. It does not
  write secrets into the guest.
- `TestGuestFilesystemOmitsProviderSecrets` already refuses plane
  xAI and GitHub values in the guest tree and env.

## Decision

**D1. Retain the refusal.** The plane does not inject a third-party
secret at wake. The guest holds only the per-turn model and git grant.

**D2. Durable secrets stay on the plane.** ADR 0009 stands. A
long-lived token in the image stays a non-goal. Receipts, logs, and
transcripts still must not print secret values.

**D3. Inject-at-wake stays refused** until a superseding ADR names
one turn that must present a third-party credential the existing
proxies cannot, and names the id, lifetime, and removal. [#503](https://github.com/Sannrox/rusui/issues/503)
remains blocked on that later choice.

## Consequences

- Easier: one guest credential. Wake, sleep, and receipts stay as
  shipped.
- Harder: a turn that needs a vendor token the proxies do not speak
  cannot run until a later ADR.
- Schema, policy, runner contract, and public API are unchanged.
  Trust model is unchanged.

## Rejected alternatives

- **Inject allowed secret ids at wake (option 1).** Adds a secret
  catalog, an inject/remove pair, and a receipt that must never leak
  the value, without a named turn that the current grant cannot serve.
  ADR 0033 already deferred third-party identity.

## Validation and reversal

Accept on merge. Validated when `DriverEnv` still exposes the turn
token as the only guest credential, and wake still writes no secret
into the environment.

Reverse with a superseding ADR that names the secret ids, the
lifetime, and the removal on sleep.

## Sources

- [#484](https://github.com/Sannrox/rusui/issues/484)
- [ADR 0009](0009-credential-broker.md), [ADR 0033](0033-third-party-identity-deferred.md)
- Decided against `main` at `6bb3436`.

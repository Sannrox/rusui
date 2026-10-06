# ADR 0061: One model upstream retained

- Status: Accepted
- Date: 2026-10-06
- Amends: [ADR 0026](0026-harness-model-upstream.md) (one
  `RUSUI_MODEL_UPSTREAM`).
- Resolves: [#496](https://github.com/Sannrox/rusui/issues/496)
- Related: [#509](https://github.com/Sannrox/rusui/issues/509)
  (named connections remain blocked).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

The plane has one model upstream and forwards the body without
choosing a provider. A guest mode that should use a different key has
no connection to name.

[#496](https://github.com/Sannrox/rusui/issues/496) asked whether an
operator may store more than one provider connection, and whether a
guest mode may name which connection the proxy uses, without the guest
seeing the key. Two options:

1. Named connections. A mode names one. The proxy uses that
   connection. The guest receives no provider key. The receipt names
   the connection id.
2. Retain one upstream.

No named project needs more than one connection. ADR 0026 already
separates harness, model, and one upstream URL. A catalog of
connections adds store, policy, and receipt shape without a second
upstream in use.

Source evidence at the commit this decision was made against:

- `ModelConfig` has one `Origin`. `ModelConfigFromEnv` reads
  `RUSUI_MODEL_UPSTREAM` only.
- Policy parse uses known fields. There is no `connections` field.

## Decision

**D1. Retain one upstream.** The proxy forwards to
`RUSUI_MODEL_UPSTREAM` or the provider default. There is no connection
catalog.

**D2. Named connections stay refused** until a superseding ADR names
the project that needs a second upstream. [#509](https://github.com/Sannrox/rusui/issues/509)
remains blocked.

## Consequences

- Easier: one URL, one key per provider, one grant swap.
- Harder: a guest mode that needs a different vendor key waits for a
  later ADR.
- Schema, policy, runner contract, and public API are unchanged.

## Rejected alternatives

- **Named connections (option 1).** Adds a catalog and a mode map
  without a named project that has two upstreams.

## Validation and reversal

Accept on merge. Validated when `ModelConfigFromEnv` still reads one
upstream URL, and policy still refuses `connections`.

Reverse with a superseding ADR that names the catalog, the mode map,
and the project that needs it.

## Sources

- [#496](https://github.com/Sannrox/rusui/issues/496)
- [ADR 0026](0026-harness-model-upstream.md)
- Decided against `main` at `111edff`.

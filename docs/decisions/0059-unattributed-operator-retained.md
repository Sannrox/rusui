# ADR 0059: Plane calls stay one unattributed operator

- Status: Accepted; amended by 0074
- Date: 2026-10-06
- Amends: [ADR 0012](0012-operator-access.md) (one operator token),
  [ADR 0039](0039-shared-operator-governance-deferred.md) (shared-
  operator governance is deferred).
- Amended by: [ADR 0074](0074-single-operator-oidc-cli.md) (credential form only).
- Resolves: [#490](https://github.com/Sannrox/rusui/issues/490)
- Related: [#128](https://github.com/Sannrox/rusui/issues/128)
  (shared-operator implementation remains blocked).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

- Amendment on acceptance: [ADR 0074](0074-single-operator-oidc-cli.md)
  permits optional OIDC CLI credentials for the same single operator.
  The token-only clauses named in ADR 0074 are superseded on acceptance;
  their text below remains historical rationale. Browser access,
  shared-operator deferral, and unattributed receipts remain unchanged.

## Context

The plane has one operator token ([ADR 0012](0012-operator-access.md),
[ADR 0039](0039-shared-operator-governance-deferred.md)). A server-side
client may call the operator API for several people. Putting that
token in a browser, or inventing a second operator, both break the
current contract.

[#490](https://github.com/Sannrox/rusui/issues/490) asked whether a
server-side caller may present the operator token and a member id,
with receipts storing the member id, while no browser receives the
token. Two options:

1. The operator token stays on the server. Receipts store the member
   id the caller supplies. The plane does not authenticate that id
   itself.
2. Retain a single unattributed operator on every call.

No named adapter needs an unauthenticated member id on receipts. A
caller-supplied id is not identity. ADR 0039 already deferred a
second operator. Attributing receipts without authenticating the id
would record a name the plane cannot stand behind.

Source evidence at the commit this decision was made against:

- `ClassifyBearer` maps the presented secret to operator, worker, or
  none. It does not read a member header.
- Operator retry receipts store actor `operator-api`. There is no
  member column.

## Decision

**D1. Retain one unattributed operator.** Every operator call authenticates the
same operator. On acceptance of [ADR 0074](0074-single-operator-oidc-cli.md),
operator API calls may use the static token or a validated OIDC access
token for the configured issuer/subject pair; the original sentence
"Every operator call is the operator token" is superseded. Receipts do not store a caller-supplied member id.

**D2. A member header is not identity.** Presenting a member id does
not authenticate, and it does not change the actor on a receipt.

**D3. Shared-operator work stays refused.** [#128](https://github.com/Sannrox/rusui/issues/128)
remains blocked on ADR 0039.

## Consequences

- Easier: one operator credential, one actor string.
- Harder: a chat adapter that wants to name a human waits for a
  later ADR that authenticates that name.
- Schema, policy, runner contract, and public API are unchanged.

## Rejected alternatives

- **Store a caller-supplied member id (option 1).** Records an
  unauthenticated name. Does not solve ADR 0039, and a browser still
  must not hold the token.

## Validation and reversal

Accept on merge. Validated when an operator retry with a member
header still records actor `operator-api`.

Reverse with a superseding ADR that names the adapter, how the
member id is authenticated, and the receipt column.

## Sources

- [#490](https://github.com/Sannrox/rusui/issues/490)
- [ADR 0012](0012-operator-access.md),
  [ADR 0039](0039-shared-operator-governance-deferred.md)
- Decided against `main` at `abf8529`.

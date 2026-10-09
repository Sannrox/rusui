# ADR 0074: Optional OIDC CLI authentication for one operator

- Status: Accepted
- Date: 2026-10-09
- Amends: [ADR 0012](0012-operator-access.md) (CLI authentication only),
  [ADR 0039](0039-shared-operator-governance-deferred.md) (token-only authentication),
  [ADR 0059](0059-unattributed-operator-retained.md) (credential form only).
- Resolves: [#595](https://github.com/Sannrox/rusui/issues/595).
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [#578](https://github.com/Sannrox/rusui/issues/578) (separate shared-actor decision).
- Discussion: none. Merging with this status is the acceptance act.
  Until merge this is a proposal, not implementation authority.

## Context

The operator currently copies a long-lived operator token to each CLI client. Provide browser-based CLI sign-in and short-lived credentials using Authorization Code + PKCE, following the Tenkai CLI approach, while retaining one operator and local-token compatibility.

## Decision

Amend ADR 0012's exclusion of OIDC and refresh tokens for CLI authentication only, ADR 0039's token-only authentication rule, and ADR 0059's requirement that every operator call present the static token. Keep their single-operator, project-policy, and unattributed-receipt rules. On acceptance,
this decision supersedes ADR 0012 D2's "No OIDC" and "no refresh-token
family" clauses for CLI credentials only, ADR 0039 D1's requirement
that plane authorization remain the one static operator token, and
ADR 0059 D1's sentence "Every operator call is the operator token."
Operator API calls may instead present a validated OIDC access token
for the configured issuer/subject pair. Prior text is preserved as
historical rationale, not an active token-only restriction on these
API calls. Browser sign-in remains static-token-only.

- Optional configuration selects one HTTPS issuer (loopback HTTP for development), audience, public client ID, scopes, and exact operator subject. The issuer and subject together identify the existing operator. No email matching, group grants, roles, operator table, or project overlays.
- The server validates signed access tokens against trusted issuer keys, issuer, audience, expiry, and configured subject. Reject unsupported algorithms, malformed tokens, missing required claims, and worker/turn credentials. Authentication performs no network requests in the request path. Discovery and key refresh use bounded requests and caching; unknown keys fail closed while refresh occurs. Startup with OIDC enabled requires usable keys.
- Serve unauthenticated public CLI metadata containing issuer, audience, client ID, and scopes only. Never expose a secret or operator subject.
- `rusui login` discovers the configuration, validates issuer discovery, opens a browser, and completes Authorization Code + PKCE S256 through a loopback callback with state checking and a bounded lifetime. Support a registered callback port and printing the URL when browser launching is unavailable.
- Cache access and refresh tokens privately per normalized server URL with atomic writes and mode 0600, pinning issuer and token endpoint at login. Refresh uses only that pinned endpoint. Login never silently changes an existing credential's issuer. `rusui logout` removes the selected local credential; it does not promise provider-side revocation.
- CLI precedence: explicit `-token`, then `RUSUI_OPERATOR_TOKEN`, then saved OIDC login for operator commands. Preserve worker-oriented command defaults. Runners and guests never use the operator cache.
- Static operator tokens remain an explicitly supported break-glass path. An issuer outage does not automatically switch credentials or approve work. Existing unexpired credentials may validate with usable cached keys; expired tokens and failed refresh require renewed login. Provider logout does not instantly invalidate an already issued JWT: bound this exposure by token expiry. Changing the configured issuer or subject rejects old OIDC credentials on restart.
- Browser console authentication remains token-based. OIDC bearer tokens do not mint console cookies, terminal leases, or preview grants in this scope. Existing token rotation behavior remains unchanged.
- Receipts retain the existing operator actor. No caller-supplied identity is recorded.

## Consequences

Essential costs: issuer validation and key rotation (server), callback/PKCE and refresh (CLI), and provider setup (operator). These replace recurring secret copying with login and automatic renewal.

Avoided costs: browser SSO, a new cookie lifecycle, roles and group mapping, multiple operators, schema changes, and a general credential framework. Integrate verification into the existing operator authorization boundary and use one shared CLI credential resolver for applicable commands.

One-time cost: configure the public client, callback, audience, subject, and access-token lifetime. Optional configuration keeps rollback to the existing operator-token path possible. Focused proof must show OIDC never grants worker or turn authority.

## Validation and reversal

Deterministic local provider fixtures cover signature, issuer, audience, subject, expiry, algorithm restrictions, key refresh failures, PKCE/state/callback handling, refresh endpoint pinning, cache permissions, per-server isolation, and precedence. Existing credential-class tests and contributor gates pass. No real provider, production instance, or daily-driver configuration is touched.

## Non-goals

Browser SSO; multiple operators; roles; group claims; audit schema changes; IdP administration; live GitHub apply; runner/guest identity; automatic fallback; deployment changes.

Disable optional OIDC configuration to return to static-token authentication.
No plane-store migration is required. Existing cached issuer credentials then
fail plane authentication; local logout removes them.

## Rejected alternatives

- Browser SSO in the same change: introduces a separate cookie and revocation lifecycle.
- Group grants or several subjects: introduces shared operators without adopting that boundary.
- An identity-aware proxy alone: does not provide CLI credential caching and refresh.
- ID tokens as API credentials: access tokens are the credential issued for the API audience.

## Sources

- [#595](https://github.com/Sannrox/rusui/issues/595).
- ADRs 0012, 0039, and 0059 linked above.
- Source baseline: `92311cb6e9d3d9ee6f6ef9bdc10ba4592981ec7e`.
- `internal/server/access.go`, `internal/server/console.go`, and
  `cmd/rusui/attach.go`: credential classes, browser sign-in, and CLI token handling.

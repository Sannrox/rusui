# ADR 0088: A configured issuer vouches for several subjects

- Status: Proposed
- Date: 2026-11-17
- Amends: [ADR 0012](0012-operator-access.md) (one operator identity),
  [ADR 0039](0039-shared-operator-governance-deferred.md) (D1, one
  operator on plane calls), [ADR 0059](0059-unattributed-operator-retained.md)
  (receipts stay unattributed), [ADR 0074](0074-single-operator-oidc-cli.md)
  (one configured subject), and opens the gate in
  [ADR 0083](0083-retain-single-operator-access.md) D2.
- Resolves: [#697](https://github.com/Sannrox/rusui/issues/697).
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0076](0076-tenant-isolation.md), [ADR 0077](0077-session-access-grants.md),
  [ADR 0078](0078-session-execution-modes.md), [#128](https://github.com/Sannrox/rusui/issues/128)
  (shared-operator governance stays closed).
- Discussion: none. Merging with this status is the acceptance act.
  Until merge this is a proposal, not implementation authority.

## Context

A service that puts its own users in front of rusui holds one operator
credential, or one static bearer per actor in `RUSUI_TENANT_TOKENS`
(ADR 0078 D3). With the first, every session and approval is the
operator. With the second, the service mints and rotates a long-lived
secret per user, and rusui cannot revoke a person without editing a
token file. Neither lets rusui name the person who approved an action on
its own receipt from a credential it verified.

ADR 0083 D2 adds a person-level actor only when a deployment needs rusui
to revoke one person or to name the approving person on a rusui receipt.
#697 names that need. ADR 0083 D3 already fixes the shape: a verified
upstream assertion carrying `sub` and `act`, not a user table, a second
static token, or a caller-supplied header (ADR 0059 D2).

ADR 0074 already validates OIDC access tokens offline against one
issuer's keys, for one configured subject that binds to tenant `default`
(ADR 0076 D1).

## Decision

**D1. Vouched subjects.** The configured OIDC issuer (ADR 0074) may vouch
for more than one subject. Optional configuration
`RUSUI_OIDC_SUBJECTS` is a JSON object, or a path to one, mapping each
`sub` to `{"tenant": "<tenant_id>", "actor": "<actor id>"}`. A token is
the operator class only when its issuer, audience, expiry, and algorithm
pass ADR 0074 validation **and** its `sub` is a key of that map. Any
other subject is refused as `CredNone`. There is no wildcard, no claim
mapping, and no group grant.

**D2. What is verified per call.** The plane verifies the signature,
issuer, audience, expiry, and `sub` on every call, offline against cached
keys as ADR 0074 specifies. The tenant and actor come only from the
configured map entry for `sub`, never from a token claim or a request
field. When the token carries an `act` claim, the plane records it as the
delegating service on the same rows as D3. It is not used for
authorization.

**D3. Recorded subject.** The resolved actor is the existing ADR 0078
actor. It is stored as `created_by` on sessions the subject starts, as
`act` on user-mode sessions, and on approval decisions, retries,
dispositions, and action receipts the subject causes, with the tenant
from D1. Receipts name the actor id, not the raw `sub` or the token.

**D4. Project binding.** A subject reaches exactly the projects and
sessions of its tenant (ADR 0076), plus sessions granted to that tenant
(ADR 0077). User-mode sessions stay visible only to their `act`
(ADR 0078). There is no per-subject project list. A deployment that needs
one subject limited to one project gives that project its own tenant.

**D5. Approvals.** A user-mode session's approvals may be decided only by
its `act`. A space-mode session's approvals may be decided by any actor
of the owning tenant, and the decision records which one.

**D6. Static credentials keep their meaning.** `RUSUI_OPERATOR_TOKEN` and
the ADR 0074 single subject stay tenant `default` and actor `default`, as
break-glass. `RUSUI_TENANT_TOKENS` keeps its meaning. A `sub` may not
also be the ADR 0074 operator subject, and two subjects may not map to
the same tenant and actor pair. Both conflicts fail closed at start.

**D7. Revocation and availability.** Removing a subject from the map and
restarting the plane refuses that subject's next call. Tokens already
issued are not tracked: exposure after removal is bounded by restart, and
after issuer-side revocation it is bounded by token expiry.

A vouched-subject token must carry `iat` and `exp`. `exp − iat` may be
at most one hour, and `iat` may be at most one minute after the plane's
clock, which is the only clock-skew allowance. A token that breaks either
rule is refused, whatever the issuer's own policy. So exposure after issuer-side revocation is at most
one hour, and it does not depend on how the issuer is configured. The
ADR 0074 single subject keeps its existing rule.

An issuer outage fails closed for vouched subjects, as ADR 0083 D3
requires, with a bound instead of at once. Keys fetched from the issuer
validate vouched-subject tokens for at most one hour after the last
successful key fetch. Once a refresh has failed past that age, every
vouched-subject call is refused until a fetch succeeds. So an outage
leaves already issued tokens usable for at most one hour, or less when
they expire sooner. The ADR 0074 single subject keeps its existing
cached-key rule. The static operator token keeps working without the
issuer.

Both one-hour bounds are fixed values, not settings. A bounded window
for stale keys of about an hour is common practice in widely used token
validation libraries. A setting waits until a deployment needs a
different value.

## Consequences

- Easier: an integrating service passes each user's own access token,
  and rusui receipts name who started, approved, or retried, from a
  credential rusui verified. Revoking one person is one map edit and a
  restart.
- Harder: the operator maintains a subject map next to the issuer. The
  issuer has to issue vouched-subject access tokens that live one hour or
  less, and an issuer outage stops vouched subjects after one hour.
- Schema: none expected. Tenant, `created_by`, and `act` columns exist
  (ADR 0076, ADR 0078). Approval decisions gain the deciding actor if the
  implementation finds it is not already stored.
- Policy: unchanged. `policy.yaml` does not name subjects.
- Trust model: a second way to reach the operator class, scoped to one
  tenant and actor. It goes through the security review path.
- Public API: unchanged routes. A refused subject is the existing 401.
- Not added: roles, teams, a user table, console sign-in by subject,
  shared-operator governance (#128 stays closed), or a guest identity
  (ADR 0033).

## Threats

| Threat | Control |
| --- | --- |
| Token replay across subjects | A token authenticates only its own `sub`; tenant and actor come from the map entry for that `sub`. Audience binds it to this plane. Replay of the same subject's token is bounded by expiry. |
| Token for another audience or issuer | Rejected by ADR 0074 validation before the map lookup. |
| Subject reaching another subject's session | Tenant scoping (ADR 0076) returns the same not-found for foreign rows. User-mode rows are visible only to their `act` (ADR 0078). Grants never expose user-mode rows (ADR 0077). |
| Approval by a subject that did not start the session | D5: user-mode approvals only by `act`. Space-mode approvals by a tenant actor are allowed and attributed. |
| Caller-asserted identity | No request field or claim selects tenant or actor (D2). The `act` claim is recorded, never trusted for access. |
| Removed subject keeps access | D7: refused after restart; outstanding tokens bounded by expiry, and vouched-subject tokens live at most one hour. |
| Issuer mints long-lived or post-dated tokens | D7: a vouched-subject token with `exp − iat` over one hour, `iat` more than one minute in the future, or no `iat` or `exp`, is refused. |
| Issuer outage | Vouched subjects are refused once the cached keys are older than one hour without a successful refresh (D7); static break-glass unaffected. |
| Map typo grants the wrong tenant | Start refuses duplicates and overlap with the operator subject (D6). Each refusal or acceptance logs the actor id, never the token. |

## Rejected alternatives

- Tenant and actor from token claims: lets the issuer's claim mapping,
  not the plane operator, decide isolation; a misconfigured claim
  crosses tenants.
- A member header next to the operator token: ADR 0059 D2; not identity.
- Per-user static bearers in `RUSUI_TENANT_TOKENS` as the only path: the
  service mints and rotates long-lived secrets per user, and nothing
  expires.
- One rusui instance per user: multiplies stores and runners.
- Token introspection per call: puts the issuer in every request path;
  ADR 0074 chose offline validation.

## Validation and reversal

Validation for the implementing change: deterministic issuer fixtures for
a mapped, an unmapped, and a removed subject; cross-subject and
cross-tenant reads returning not-found; user-mode approval by a non-`act`
refused; space-mode approval attributed; conflicting map entries refused
at start; with the issuer unreachable, a vouched-subject token validating
before the key-age bound and refused after it, while the static token
works; a vouched-subject token refused when `exp − iat` exceeds one
hour, when `iat` is more than one minute in the future, or when it lacks
`iat` or `exp`; receipts naming the actor id and
never the `sub` or token. Existing
credential-class and tenant tests pass unchanged.

Reversal: unset `RUSUI_OIDC_SUBJECTS`. Vouched subjects are refused and
the ADR 0074 single subject and static tokens behave as before. Recorded
actor ids stay on existing rows as history.

## Sources

- [#697](https://github.com/Sannrox/rusui/issues/697).
- ADRs 0012, 0039, 0059, 0074, 0076, 0077, 0078, 0083 linked above.
- Source baseline: `89501fa069304d2a5b5f61e33a3cc9d9c63b288e`.
- `internal/server/access.go` (`lookupPrincipal`, `tenantForToken`,
  `actorForToken`) and `internal/oidc/verifier.go` (one subject).

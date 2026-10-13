# ADR 0076: Tenant isolation on the session plane

- Status: Accepted
- Date: 2026-10-13
- Amends: [ADR 0012](0012-operator-access.md) (operator credentials
  carry a tenant isolation key); [ADR 0005](0005-policy-v2-project.md)
  (a project may name its tenant).
- Resolves: [#604](https://github.com/Sannrox/rusui/issues/604).
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [sekai-chisei ADR 0099](https://github.com/Sannrox/sekai-chisei/blob/main/docs/decisions/0099-shared-identity-and-tenancy.md),
  [#578](https://github.com/Sannrox/rusui/issues/578),
  [#601](https://github.com/Sannrox/rusui/issues/601).
- Discussion: none. Merging with this status is the acceptance act.
  Until merge this is a proposal, not implementation authority.

## Context

Rusui's unit is the project (ADR 0005). Credentials are one operator
token, one worker secret, and per-turn grants (ADR 0012), with optional
OIDC for that same operator (ADR 0074). A caller in front of Rusui that
serves several tenants therefore presents one operator credential and
must filter sessions and approvals after the fact. A bug in that caller
exposes another tenant's work.

The shared identity contract (chisei ADR 0099) names `tenant_id` as the
hard isolation boundary. Rusui must enforce it on the session plane.

## Decision

`tenant_id` is stored on every session. Approvals inherit it through
the session. List, read, decide, review, retry, disposition, and
measurement reads for operator credentials are scoped to that key.
Unknown and forbidden session, approval, repository, or result IDs
return the same not-found response.

1. **Default tenant.** A single-operator install uses tenant `default`.
   `RUSUI_OPERATOR_TOKEN` and a validated OIDC operator token bind to
   it. Schema v46 backfills existing rows to `default`. That is the
   known binding for this install, not an unknown-tenant guess.
2. **Per-tenant operator credentials.** `RUSUI_TENANT_TOKENS` is a JSON
   object of `tenant_id` to bearer, or a path to that file. Each token
   is the operator class for that tenant only. It cannot mint console
   cookies. The `default` key is refused; overlap with the operator
   token or worker secret fails closed at start. Two tenants may not
   share a token.
3. **Tenant-scoped projects.** Optional project `tenant` in
   `policy.yaml` stamps sessions created in that project. Empty means
   `default`. A child session stays in the parent's tenant; a
   cross-tenant child is refused. Changing a project's tenant does not
   restamp existing sessions; admission and claim refuse the mismatch.
4. **Worker and turn.** The worker secret and turn grants remain
   plane-internal and are not tenant credentials. Membership and
   product access stay upstream.

## Consequences

A multi-tenant caller holds one credential per tenant. Session listings,
approval queries, review and retry mutations, dispositions, and
project measurements are scoped inside Rusui. Single-operator installs
keep one token and see every default-tenant row.

Schema v46 is a migration. Rollback after tenant-qualified writes must
keep tenant columns. This is not a second human operator, a role table,
or shared-operator governance (ADR 0039 / #578).

## Rejected alternatives

- **Caller-side filtering only.** The issue is that a bug in the caller
  would leak another tenant's sessions and approvals.
- **Tenant as a request header.** An untrusted field is not the
  isolation key (chisei ADR 0099).
- **Operator token as a superuser.** The default token is the default
  tenant, not break-glass across tenants.
- **Namespace or project as the isolation key.** A project may belong
  to a tenant; it is not the tenant.

## Validation and reversal

Focused tests show tenant A's credential cannot list, read, decide,
review, retry, dispose, or measure tenant B's work, and that a
single-operator token still reaches default-tenant rows. Revert by
dropping the extra tokens and leaving `tenant_id` unused; do not rewrite
rows across tenants.

## Sources

- [#604](https://github.com/Sannrox/rusui/issues/604)
- [sekai-chisei#1397](https://github.com/Sannrox/sekai-chisei/issues/1397)
- [ADR 0012](0012-operator-access.md), [ADR 0005](0005-policy-v2-project.md)

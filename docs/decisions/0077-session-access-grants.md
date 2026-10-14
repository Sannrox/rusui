# ADR 0077: Sessions are tenant-owned; cross-tenant access is an audited grant

- Status: Accepted; amended by 0078
- Date: 2026-10-13
- Amends: [ADR 0076](0076-tenant-isolation.md) (by-id read and reopen
  may follow an explicit grant); [ADR 0012](0012-operator-access.md)
  (owner-only grant administration).
- Resolves: [#606](https://github.com/Sannrox/rusui/issues/606).
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0076](0076-tenant-isolation.md).
- Discussion: none. Merging with this status is the acceptance act.
  Until merge this is a proposal, not implementation authority.

## Context

Tenant isolation (ADR 0076) scopes operator lists and mutations to
`tenant_id`. Sessions still had no recorded creator, and a caller that
knew a session id had no documented way to share that one session
across tenants. Threads in Rusui are the session conversation
(`guest_session_id`), not a second table; ownership of a thread is
ownership of the session.

## Decision

Every session is a tenant-owned resource with a stable id and a
recorded `created_by`. Schema v47 backfills `created_by` from
`tenant_id`. Sharing inside the owning tenant is the existing tenant
credential. Sharing across tenants is an explicit, audited row in
`session_access_grants`.

1. **Creator.** `created_by` is stamped when the session is created and
   is not rewritten when `tenant_id` later changes.
2. **List.** `GET /sessions` stays owner-tenant only. A live grant does
   not add the session to another tenant's list.
3. **By-id access.** `GET /sessions/{id}` and reopen
   (`POST /sessions/{id}/unarchive`) succeed for the owning tenant, the
   worker, or a tenant with a live grant. Unknown and forbidden ids
   return the same not-found response.
4. **Mutations and approvals.** Cancel, archive, follow-up, children,
   workspace writes, webhook mint, and approval decide stay with the
   owning tenant. Grants do not transfer those rights.
5. **Grant administration.** Only the owning tenant lists, creates, or
   revokes grants. Revoke is append-only (`revoked_at`); a later grant
   to the same tenant is a new live row. Self-grants are refused.

## Consequences

A tenant can reopen a named session another tenant granted without
seeing that tenant's catalog. Grant rows are an audit trail. Schema v47
is a migration; rollback after grant writes must keep the table and
`created_by`. This is not a second operator, a role table, or
shared-operator governance (ADR 0039).

## Rejected alternatives

- **Grant includes list.** A catalog leak would undo tenant isolation
  for every granted session.
- **Grant includes decide and cancel.** Sharing read/reopen is not
  transferring ownership.
- **Separate thread table.** The conversation is already the session
  (`guest_session_id`).
- **Header-based share token.** An untrusted field is not the
  isolation key (ADR 0076).

## Validation and reversal

Focused tests show a foreign tenant cannot reopen by id until a grant
exists, can after the grant, cannot after revoke, and never sees the
session in list. Revert by ignoring grant rows and leaving
`created_by` unused; do not rewrite tenant columns.

## Sources

- [#606](https://github.com/Sannrox/rusui/issues/606)
- [ADR 0076](0076-tenant-isolation.md)

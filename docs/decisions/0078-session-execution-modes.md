# ADR 0078: Session execution modes: user vs space

- Status: Accepted; amended by 0079, 0081
- Date: 2026-10-13
- Amends: [ADR 0076](0076-tenant-isolation.md) (visibility inside a
  tenant); [ADR 0077](0077-session-access-grants.md) (user-mode rows
  are not shared by grant).
- Resolves: [#605](https://github.com/Sannrox/rusui/issues/605).
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [sekai-chisei#1398](https://github.com/Sannrox/sekai-chisei/issues/1398).
- Discussion: none. Merging with this status is the acceptance act.
  Until merge this is a proposal, not implementation authority.

## Context

Tenant isolation (ADR 0076) scopes operator work by `tenant_id`. It does
not say whether a session acts as the person who started it or as a
shared project runtime, so another member of the same tenant can read
every transcript. The shared identity contract names `act` as the
delegating actor. Rusui must record that choice on the session.

`sessions.mode` is the guest effort catalog (low/medium/high/ultra) and
is not this field.

## Decision

Every session stores an immutable `execution_mode` chosen at creation,
and an `act` principal. Schema v48 backfills existing rows to space
mode with `act` copied from `created_by`.

1. **User mode.** The session runs as the creating member. `act` is that
   member. Logs and transcript reads, including list, are visible only
   to that actor inside the tenant. Cross-tenant grants do not expose
   user-mode sessions.
2. **Space mode.** The session is a runtime principal bound to the
   project/tenant. Members of the owning tenant may read logs. Grants
   from ADR 0077 still apply.
3. **Credentials.** `RUSUI_TENANT_TOKENS` may map a tenant to one bearer
   (actor equals tenant) or to a map of actor id to bearer. Identity
   comes from the credential, not a request field (ADR 0059).
4. **Tools.** Claim and `DriverEnv` copy `execution_mode` and `act` as
   `RUSUI_EXECUTION_MODE` and `RUSUI_ACT`.

## Consequences

A tenant can host personal and shared sessions without a second
operator table. Schema v48 is a migration; rollback after mode-qualified
writes must keep the columns. Space-mode write policy is
[ADR 0079](0079-space-mode-read-only-tools.md).

## Rejected alternatives

- **Reuse `sessions.mode`.** That column is the ACP effort catalog.
- **Caller-supplied member header.** An untrusted field is not identity.
- **Default existing rows to user mode.** That would hide current
  tenant-wide transcripts after upgrade.

## Validation and reversal

Focused tests create each mode, return it on GET, reject changes, deny
another member's user-mode transcript, and allow space-mode reads.
Revert by ignoring the columns; do not rewrite `tenant_id`.

## Sources

- [#605](https://github.com/Sannrox/rusui/issues/605)
- [sekai-chisei#1398](https://github.com/Sannrox/sekai-chisei/issues/1398)
- [ADR 0076](0076-tenant-isolation.md), [ADR 0077](0077-session-access-grants.md)

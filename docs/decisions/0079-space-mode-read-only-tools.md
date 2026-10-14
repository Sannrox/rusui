# ADR 0079: Space-mode sessions default to read-only tools

- Status: Accepted
- Date: 2026-10-14
- Amends: [ADR 0078](0078-session-execution-modes.md) (space-mode
  write policy).
- Resolves: [#608](https://github.com/Sannrox/rusui/issues/608).
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0005](0005-policy-v2-project.md).
- Discussion: none. Merging with this status is the acceptance act.
  Until merge this is a proposal, not implementation authority.

## Context

Space-mode sessions (#605) are a shared project runtime. Policy
permission rules can still allow write-capable ACP tools (`execute`,
`edit`, and other non-read kinds) for every member who can see the
session. That widens blast radius for a principal that is not a person.

User-mode defaults stay out of scope.

## Decision

1. A space-mode session may use read-only or stateless ACP kinds
   (`read`, `search`, `fetch`, `think`) under existing policy rules.
2. Write-capable or stateful kinds (`execute`, `edit`, `delete`,
   `move`, `other`, or a missing kind) are denied until the owning
   tenant records a live per-session tool grant. A grant names one kind
   or `write` (every write kind). Policy rules and the implement fence
   still apply after a grant.
3. Grants and revokes are append-only rows plus an action receipt.
   The active tool set is returned on session GET and shown in the
   console.
4. Implement (pinned task) sessions receive a `write` grant at create
   so publication stays possible under the existing fence. User-mode
   sessions ignore this table.

## Consequences

New space-mode runtimes cannot shell or edit until an operator grants
tools. Schema v49 adds `session_tool_grants`. Rollback after grant
writes must keep the table.

## Rejected alternatives

- **Ask-on-write instead of deny.** The issue requires the session
  cannot invoke the tool until granted.
- **Change policy.yaml defaults.** The restriction is per session, not
  per project.

## Validation and reversal

Focused tests deny execute on a new space session, allow it after a
grant, deny it after revoke, and record grant/revoke receipts. Revert
by ignoring the table.

## Sources

- [#608](https://github.com/Sannrox/rusui/issues/608)
- [ADR 0078](0078-session-execution-modes.md)

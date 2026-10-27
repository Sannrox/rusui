# ADR 0081: Session retention: short-lived vs indefinite

- Status: Accepted
- Date: 2026-10-14
- Amends: [ADR 0078](0078-session-execution-modes.md) (retention is
  chosen per session, including user vs space).
- Resolves: [#609](https://github.com/Sannrox/rusui/issues/609).
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0076](0076-tenant-isolation.md),
  [ADR 0079](0079-space-mode-read-only-tools.md).
- Discussion: none. Merging with this status is the acceptance act.
  Until merge this is a proposal, not implementation authority.

## Context

Sessions persist prompts, attachments, and guest pins for as long as
the SQLite file lives. #605 split execution into user and space modes;
operators still need a retention class so a session is either
short-lived or kept indefinitely. Indefinite retention widens the
window for write-capable tools.

Legal hold is out of scope.

## Decision

1. Every session has a retention class, scoped to its tenant (#604)
   and chosen per execution mode (#605): `short` (expires after 72h)
   or `indefinite`.
2. Existing sessions and new sessions default to `short` so current
   space-mode grants keep working across the upgrade.
3. After the TTL, expiry archives the session, removes session data
   (prompt, guest pin/config, attachments), and records
   `retention.expire`. Explicit deletion does the same as
   `retention.delete`. Session and environment rows stay.
4. An indefinite session cannot use write-capable or stateful ACP
   tools without an explicit audited override (`retention.write_override`),
   in addition to space-mode tool grants (ADR 0079) when the session
   is space-mode.

## Consequences

Schema v50 adds `session_retention`. Rollback after expiry cannot
restore wiped prompt bytes from SQLite. Operators who need a durable
workspace must set `indefinite` and record a write override before
granting write tools.

## Rejected alternatives

- **Legal hold.** Out of scope for this issue.
- **Per-project default in policy.yaml.** Retention is per session.
- **Destroy the session row on expiry.** Archive already keeps the
  identity (#499); expiry removes data, not the id.

## Validation and reversal

Focused tests show short-lived data gone after TTL with an expire
receipt, and indefinite sessions refuse write-capable tools without
an override. Revert by ignoring the table and gate.

## Sources

- [#609](https://github.com/Sannrox/rusui/issues/609)
- [ADR 0078](0078-session-execution-modes.md)
- [ADR 0079](0079-space-mode-read-only-tools.md)

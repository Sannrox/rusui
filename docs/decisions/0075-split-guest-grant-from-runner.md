# ADR 0075: Guest grant is model and git only; runner token records actions

- Status: Accepted
- Date: 2026-10-13
- Amends: [ADR 0009](0009-credential-broker.md) (one per-turn grant no
  longer authenticates both guest proxies and runner-owned plane
  writes); [ADR 0012](0012-operator-access.md) D1 (turn class splits
  into runner token and guest grant).
- Resolves: [#601](https://github.com/Sannrox/rusui/issues/601).
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md).
- Discussion: none. Merging with this status is the acceptance act.
  Until merge this is a proposal, not implementation authority.

## Context

Claim minted one per-turn secret, hashed into both `turn_credentials`
and `credential_grants`. The runner posted transcript, permission, and
approval rows with that secret. `DriverEnv` also copied it into the
guest as `RUSUI_TURN_TOKEN` and as the model API key. Child processes
could `POST /turns/{id}/actions` with no body cap, no type allow-list,
and no per-turn budget, forging `acp.session.update` and
`acp.approval` rows that skip runner redaction.

## Decision

Mint two secrets at claim and on lease replay.

1. **Runner turn token** lives only in `turn_credentials`. It
   authenticates heartbeat, complete, fail, terminal, schedule, session
   events, approval polling, and `POST /turns/{id}/actions`. The runner
   keeps it. The guest environment never receives it.
2. **Guest grant** lives only in `credential_grants` (`kind=turn`). It
   authenticates the model proxy and git proxy. `DriverEnv` copies it
   as `RUSUI_TURN_TOKEN` and as the guest's model API key / git
   extraHeader. It cannot post actions, complete a turn, or poll
   approvals.

`POST /turns/{id}/actions` additionally:

- caps the body at 64 KiB;
- allows only known action types;
- runs `NormalizeSessionUpdate` and redaction on
  `acp.session.update` before insert;
- refuses a turn after 4096 rows or 4 MiB of stored bodies.

Heartbeat still renews both secrets' TTLs.

## Consequences

Guest code that reused `RUSUI_TURN_TOKEN` to talk to runner-owned
endpoints now receives 401. Claim JSON gains `guest_grant`; runners
older than this change would put an empty grant in the guest and break
model calls, so plane and runner ship together.

No extra table: the split is which table hashes the secret. Schema v45
adds `actions_turn` so the budget check does not scan the session
transcript.

## Rejected alternatives

- **Allow-list only, keep one secret.** The guest would still hold a
  credential that can post runner-owned types.
- **Worker secret for action ingest.** The issue keeps a runner turn
  token for `/turns/{id}/actions`, approvals, and the terminal.
- **New grant kind.** `GrantTurn` already means guest model and git
  access; putting the runner token in that table would restore the
  bypass.

## Validation and reversal

Focused tests show the guest grant is 401 on `/turns/{id}/actions` and
`/jobs/{id}/complete`, the runner token records a normalized redacted
session update, oversized bodies are 413, unknown types are 400, and
the per-turn budget is 429. Model and git proxy tests authenticate with
the guest grant. Revert by minting one secret into both tables again.

## Sources

- [#601](https://github.com/Sannrox/rusui/issues/601)
- [ADR 0009](0009-credential-broker.md)

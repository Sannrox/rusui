# ADR 0019: One Rusui attach command selects the runtime-owned transport

- Status: Accepted
- Date: 2026-09-24
- Resolves: [Issue #185](https://github.com/Sannrox/rusui/issues/185)
- Discussion: none. Merging with this status is the acceptance act; the
  decision stays inside the accepted ADR 0016 boundary.
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0016](0016-local-interactive-runtime.md)

## Context

Rusui has a `rusui attach SESSION_ID` command, but it currently streams
Session and Turn events. It does not provide the interactive attachment that
the local Sumika profile and managed terminal each own.

The accepted local boundary keeps PTY ownership and bytes with Sumika. The
managed terminal keeps authorization, write leases, environment isolation,
expiry, and audit in Rusui. Routing both profiles through one byte proxy would
break the local boundary; giving them separate commands would make the
operator select runtime details that Session metadata already identifies.

## Decision

- `rusui attach SESSION_ID` is the first supported cross-runtime attach client.
  It reads the authenticated Session detail first and reports Project,
  Session, Environment, runtime, current Process (or that none is live), and
  current Turn (or none).
- For a local Session, the command must run as the Sumika daemon's OS user on
  the same host. It reads the active policy revision and opens the existing
  SQLite store, then uses `Engine.AttachLocalSession` to validate the current
  Process and policy, record the Attach generation, and connect directly to
  Sumika's same-user Unix socket. PTY bytes never pass through Rusui HTTP.
  The local terminal is raw, terminal-size changes use Sumika's existing
  Resize operation, and `Ctrl+]` detaches without sending that byte to the
  Process.
- For a managed Session, the command uses the existing authenticated console
  terminal page, output stream, and line-input endpoint. It acquires a write
  lease only when one is free; an existing writer leaves the CLI read-only.
  The command releases only the lease generation it acquired. The browser
  terminal and ACP editor contracts remain unchanged.
- Before opening a managed terminal, the command replays and follows the
  existing Session, Turn, and action events from `GET /sessions/{id}/attach`.
  It keeps the session id and environment; it does not create either object.
- A stale Process, expired Environment, denied authorization, or disconnected
  transport is reported as an error. The command never falls back to another
  runtime, Process, host, or transport.

## Consequences

The operator uses one command while each runtime retains its own attachment
authority. Local attachment shares Rusui's existing SQLite database and policy
revision, so no second durable store or new local PTY protocol is introduced.
The operator must pass `-db` when the server uses a non-default database path;
local attachment is unavailable off the Sumika host. Managed attachment keeps
its current line-oriented input semantics and audited write lease.

## Rejected alternatives

- **Proxy local PTY bytes through Rusui HTTP.** This makes local terminal data
  transit the control plane and conflicts with ADR 0016's ownership boundary.
- **Require a runtime-specific command.** Separate local and managed commands
  duplicate discovery and make the operator choose a runtime manually.
- **Use the worker token for attachment.** Workers can inspect Session state,
  but terminal control requires the distinct operator authority in ADR 0012.

## Validation and reversal

CLI blackbox checks cover operator authentication, metadata reporting, direct
local Attach and detach, managed lease acquisition, line input, audit, and
generation-fenced release. Existing engine, Sumika protocol, server, browser
terminal, and ACP tests cover resize, attach stealing, stale local state, lease
refusal, and expired Environments. CLI blackbox checks also cover dead
and lost local Processes, a missing Sumika daemon, bad operator tokens, and
an expired managed Environment. Reverse by superseding this ADR; no data
migration is required.

## Sources

- [Issue #185](https://github.com/Sannrox/rusui/issues/185)
- [ADR 0012: operator access](0012-operator-access.md)
- [ADR 0016: local interactive runtime boundary](0016-local-interactive-runtime.md)
- [Issue #184](https://github.com/Sannrox/rusui/issues/184)
- [Sumika protocol baseline at commit 3dadc7a](https://github.com/Sannrox/sumika/commit/3dadc7a97fba2aaaa53aea767f269dfa6c1cfff0)

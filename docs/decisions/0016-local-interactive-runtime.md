# ADR 0016: Local interactive runtime boundary with Sumika

- Status: Accepted
- Date: 2026-09-23
- Resolves: [#182](https://github.com/Sannrox/rusui/issues/182)
- Narrows: [#183](https://github.com/Sannrox/rusui/issues/183)
- Related: [ADR 0001](0001-environment-plane.md) (environment plane),
  [ADR 0003](0003-operator-surface.md) (one object API),
  [ADR 0011](0011-unattended-session-contract.md) (unattended session and
  turn), [ADR 0012](0012-operator-access.md) (managed terminal and preview),
  [ADR 0015](0015-agent-publication.md) (implement sessions), and Sumika
  [ADR 0004](https://github.com/Sannrox/sumika/blob/main/docs/decisions/0004-project-is-a-grouping-key.md)
  (project grouping) and
  [ADR 0005](https://github.com/Sannrox/sumika/blob/main/docs/decisions/0005-pairing-token-remote-attach.md)
  (opt-in remote attach).
- Discussion: none. GitHub Discussions are disabled. The decision is recorded
  here and in the contract. Merging with this status is the acceptance act.

## Context

Rusui owns durable work: projects, sessions, environments, policy, turns,
leases, credentials, and receipts. Its managed profile runs agents in managed
environments and gives an operator controlled terminal and ACP editor access
([ADR 0012](0012-operator-access.md)).

Sumika owns interactive local processes. Its daemon keeps a named PTY child
alive across client detach. The local protocol is JSON-lines over a
same-user Unix socket; attach then carries raw PTY bytes. Attach is exclusive:
a new client steals the previous one, while the child keeps running. Sumika
reports hook observations as running, idle, or blocked; without an observation
the status is unknown. A dead child stays dead until an explicit restart.

Sumika now has an optional project string for display grouping
([ADR 0004](https://github.com/Sannrox/sumika/blob/main/docs/decisions/0004-project-is-a-grouping-key.md)).
That string is not a project record or policy identity. Sumika also has an
accepted opt-in remote attach trust model, but this Rusui integration uses only
the same-host local socket.

The products use the word Session for different objects. A Rusui Session is
durable work on a Project and Environment. A Sumika Session is a live PTY
Process. Treating them as the same object would mix durable work, leases,
terminal control, and local process lifetime.

Rusui already has a local environment driver for trusted execution. This ADR
defines a separate, experimental local session kind that uses Sumika; the
shared word local does not make the two runtime paths interchangeable.

## Decision

Rusui remains the control plane and only durable source of truth. Sumika is the
local process supervisor. Rusui owns policy and durable work identity; Sumika
owns the live Process, local Attach, and PTY. Rusui records Sumika's
observations as events and never copies Sumika's authoritative runtime state.

### D1. Authority and identity

| Object | Authority | Other side holds |
| --- | --- | --- |
| Project | Rusui | Sumika may receive its name as an optional display grouping string; that string grants no policy authority |
| Session | Rusui for durable work | Sumika holds a Process name and optional display grouping string |
| Environment | Rusui | For local sessions, it identifies the operator's host; it does not imply an isolated machine |
| Process | Sumika for the live PTY child | Rusui stores a durable identity and revisioned lifecycle observation per generation |
| Turn | Rusui for unattended review, run, and scheduled work | A local session has no Turn |
| Attach | Sumika for a local Process; Rusui for a managed terminal | Neither side proxies the other's attachment |
| Status | Sumika observes; Rusui stores timestamped reports | A stored report is evidence of an observation, not current truth |
| Terminal bytes | Sumika, in memory | Rusui never relays or stores local PTY bytes |
| Policy decisions and receipts | Rusui | Sumika holds none |

In Rusui vocabulary, a Sumika Session is a Process, never a Rusui Session.
Sumika's optional project string is only a grouping projection of the Rusui
Project name.

### D2. A separate local session kind

Local interactive work uses a new session kind, local, not run. A run session
means unattended work with turns, lease budgets, and managed credentials. A
local session is human-driven and must not acquire those authorities.

- Policy must explicitly enable local for a Project. It is absent from the
  defaults. The current policy parser and runtime do not implement this kind;
  follow-up work must add it fail-closed.
- Local sessions create no Turn, lease, retry budget, per-turn grant, model
  proxy credential, or GitHub credential. They are never implement sessions.
- Pause prevents new local sessions but does not kill a running Process.
  Cancellation asks Sumika to kill the Process; Rusui records completion only
  after Sumika reports it dead.
- A local Environment means the operator's host under the Sumika daemon's OS
  identity. Multiple local sessions may use that same physical host. It is
  not a private VM, container, or OS isolation boundary.

### D2a. Rusui Process and Attach records

Issue #183 adds an additive, versioned Rusui record model. It records identity
and accepted observations without taking ownership of Sumika's live runtime:

- Each local Session may have successive Process records. A record has its own
  ID, per-Session generation, stable Sumika name, latest observed state, and
  optimistic revision. A new generation may start only after the prior one is
  confirmed dead or lost. Unknown remains active and prevents duplicate start.
- Each successful local Attach observation has a Process ID and generation,
  its own Attach generation, state, and revision. It stores no client token,
  socket, or PTY data. A successful new Attach marks the prior attached or
  unknown observation stolen; a stale disconnect can end only its exact
  generation.
- Detach changes only the Attach observation. Process death or loss closes its
  attached or unknown Attach observation as process_exited. Neither operation
  mutates the durable Session, a Turn, or the managed terminal lease.
- Plane restart and database restore change current running/attached
  observations to unknown and advance their revisions. They do not infer
  process death, discard history, or permit a second start. Reconciliation
  must accept a current Sumika observation before the state can advance.
- Session detail returns Process and nested Attach records separately from
  Turns and Environment state. It includes identifiers and observations only;
  it does not expose argv, cwd, socket details, or PTY bytes.

These rows are Rusui observation records, not a second process or attachment
authority. Sumika still owns the live child, the actual writer connection, and
the PTY.

### D3. Lifecycle mapping

| Event | Sumika | Rusui |
| --- | --- | --- |
| Start | Starts the requested argv and cwd under a unique name | Creates the local Session first, then requests the Process and records the result |
| Running, idle, or blocked | Reports a hook observation | Updates the matching Process ID, generation, and revision |
| No report or daemon unavailable | Has no current observation | Exposes unknown or unavailable; a stale report is not treated as current |
| Detach | Ends the client connection | No Session or Process change |
| Reconnect | Attaches to the same named Process | Records a new Attach generation; no new Session or Turn |
| Steal | New Attach disconnects the previous Attach | Marks the previous Attach observation stolen; Process and Session continue |
| Process death | Reaps the child and reports dead | Records dead and closes its current Attach observation as process_exited; the durable Session remains inspectable |
| Daemon restart | Rebuilds an empty in-memory Process table | Marks Process and Attach observations unknown and advances revisions; it does not infer death or restart a possibly orphaned child |
| Explicit restart | Starts the named Process only after the prior instance is confirmed dead or lost | Records the next Process generation against the same durable Session |
| Cancel | Attempts to kill the named Process | Records cancel_requested and confirms cancellation only after Sumika reports dead |
| Environment replacement | Not applicable to the local host | No effect on managed Environment replacement behavior |
| Sleep or wake | Not controlled by Rusui | No local Process transition is inferred |
| Expiry | No automatic local Process expiry | The local Session remains until explicit operator action |

If Sumika is unavailable during cancellation, Rusui keeps the cancellation
unconfirmed for reconciliation. It must not report a stopped Process based only
on a failed socket request.

### D4. Names, collisions, and reconciliation

- The Process name is rusui- followed by the Rusui Session's numeric ID. Rusui
  Session IDs are stable SQLite auto-increment identities. Each restart
  generation has a separate Rusui record but reuses this Sumika name only
  after the prior process is confirmed dead or lost. This name fits
  Sumika's 64-character limit and permitted character set without exposing a
  repository name.
- Rusui may pass the Project name in Sumika's optional project field for picker
  grouping. It is display metadata only and is never used to authorize work.
- Sumika may return an existing live Process for a duplicate name without
  checking that argv and cwd match. Before associating it with a Rusui Session,
  Rusui must compare the returned name, argv, and cwd. A mismatch is a
  collision: record it and do not attach or kill the Process.
- At startup and on a bounded cadence, Rusui lists Sumika Processes. A
  rusui-named Process with no open local Session is an orphan. Rusui reports
  it and does not kill it automatically because it may contain unsaved work.
- An open local Session whose Process is absent is recorded as process_lost.
  Restart is explicit; Rusui never silently substitutes a different Process.
- Processes without the rusui- prefix belong to the operator and are ignored.

### D5. Protocol boundaries

- The local profile uses Sumika's existing same-host, same-user Unix socket
  protocol. Rusui uses Start, List, Attach, and Kill; no new wire protocol or
  second store is introduced. Sumika keeps its in-memory Process table and
  local configuration. Rusui keeps durable Sessions, policy, events, and
  receipts.
- Rusui does not use Sumika's opt-in remote pairing transport. Remote Sumika
  integration, remote Process migration, and pairing-token delegation are
  deferred.
- The managed profile is unchanged. Runners, containers, managed terminal
  write leases, and the ACP editor remain Rusui-owned paths.

### D6. Credentials and trust

A local Process runs as the Sumika daemon's OS user. Sumika launches it with
the daemon's inherited environment plus its session and terminal variables;
the current Start protocol has no environment or credential field. The
Process can use local CLI authentication and access same-user files permitted
by the operating system. This is ambient host authority, not a Rusui-brokered
credential and not a sandbox.

Rusui injects no turn grant, model proxy token, GitHub credential, or other
managed secret into a local Process. This local trust profile is outside the
managed guest's credential guarantee and must be described as experimental.
Managed credentials continue to follow ADR 0009 and ADR 0015.

### D7. Existing operator surfaces

The managed terminal ([ADR 0012](0012-operator-access.md), #114) and ACP
editor shim (#116) remain managed-environment features. Local terminal input
uses Sumika Attach; Rusui does not proxy it through the browser or console.
Rusui may later show local Session metadata and the last observed status
read-only, but it does not expose local PTY bytes.

### D8. Scope

- Core: none yet. The local profile is not part of the supported 1.0 core;
  decision #141 must explicitly include it before that claim changes.
- Experimental: the local session kind, same-user Sumika start/list/kill
  integration, status events, and orphan reporting. Issue #183 adds only the
  additive Process/Attach observation records and session detail fields; no
  local adapter is implemented yet.
- Deferred: Sumika remote pairing from Rusui, remote hosts, shared Attach,
  console Attach to local PTYs, automatic orphan cleanup, and any claim that
  local execution has managed-container isolation.

## Consequences

Rusui and Sumika retain one authoritative store each for the state they own.
The durable Rusui Session survives client detach and Process loss, while
Sumika's Process and Attach remain local runtime state. Managed sessions keep
their existing turns, credential rules, terminal lease, and ACP editor.

The local path has weaker isolation by design: the agent process has the
Sumika daemon's OS identity and may access local authentication material.
Daemon restart can also leave an untracked child, which requires operator
reconciliation rather than automatic cleanup.

## Options considered

Option A: Treat Sumika as a Rusui runner and reuse run sessions. This keeps one
session kind but couples a human-driven PTY to unattended turns, lease budgets,
and managed credentials. It also moves policy responsibilities into a local
process supervisor.

Option B: Keep durable work in Rusui and live Process and Attach ownership in
Sumika, with a separate local session kind and an explicit same-user trust
profile. This requires a small adapter and reconciliation loop, while keeping
the existing managed credential and lease boundaries intact.

Recommendation: Option B. It gives each lifecycle one owner, avoids a second
policy or receipt store, and makes the local host trust boundary explicit.

## Validation and reversal

This decision was checked against Rusui's SQLite auto-increment Session IDs,
current policy defaults, managed terminal and credential contracts, and
Sumika's current protocol and implementation at
[commit 3dad7a9](https://github.com/Sannrox/sumika/commit/3dad7a97fba2aaaa53aea767f269dfa6c1cfff0).
That implementation has an optional project grouping field, a 64-character
restricted Process name, a same-user local socket, an in-memory Process table,
and exclusive Attach stealing. The decision does not claim that a Rusui local
adapter exists or that the lifecycle has passed an end-to-end runtime test.
Issue #183 adds an additive v17 schema and session detail API for Process and
Attach observation records. Issues #184–#186 own the adapter and cross-profile
evidence; #141 controls stable core promotion. Reverse by superseding this ADR
before any core promotion; this decision itself adds no runtime schema.

## Sources

- [#182](https://github.com/Sannrox/rusui/issues/182)
- Rusui [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [CONTEXT.md](../../CONTEXT.md), [ADR 0011](0011-unattended-session-contract.md),
  [ADR 0012](0012-operator-access.md), and [ADR 0015](0015-agent-publication.md)
- Sumika [CONTEXT.md](https://github.com/Sannrox/sumika/blob/main/CONTEXT.md),
  [ADR 0004](https://github.com/Sannrox/sumika/blob/main/docs/decisions/0004-project-is-a-grouping-key.md),
  [ADR 0005](https://github.com/Sannrox/sumika/blob/main/docs/decisions/0005-pairing-token-remote-attach.md),
  and [protocol](https://github.com/Sannrox/sumika/blob/3dad7a97fba2aaaa53aea767f269dfa6c1cfff0/crates/sumika-protocol/src/lib.rs)

# ADR 0016: Local interactive runtime boundary with Sumika

- Status: Proposed
- Date: 2026-09-23
- Resolves: [#182](https://github.com/Sannrox/rusui/issues/182)
- Related: [ADR 0001](0001-environment-plane.md) (environment plane),
  [ADR 0003](0003-operator-surface.md) (one object API),
  [ADR 0011](0011-unattended-session-contract.md) (session and turn),
  [ADR 0012](0012-operator-access.md) (terminal and preview),
  [ADR 0015](0015-agent-publication.md) (implement sessions);
  [Sumika](https://github.com/Sannrox/sumika) `CONTEXT.md` and its
  ADR 0003 "cwd is not a project".
- Discussion: none. GitHub Discussions are disabled. Merging with status
  Proposed records the investigation. Accepting this file is a separate
  maintainer act; until then [ARCHITECTURE.md](../../ARCHITECTURE.md)
  does not include the local runtime.

## Context

rusui owns durable work: projects, sessions, turns, environments, policy,
leases, credentials, and receipts. It runs agents unattended in managed
environments and lets an operator attach a terminal
([ADR 0012](0012-operator-access.md)) or an ACP editor to them.

Sumika owns interactive agent processes on the operator's own machine. A
daemon keeps a named PTY child `{name, argv, cwd}` alive across terminal
closes. Its protocol (JSON lines over a same-uid Unix socket) has `start`,
`list`, `attach` (exclusive; a second attach steals), `resize`, `kill`,
`report` (`idle` / `blocked` / `running` from vendor hooks, else
`unknown`), and `scrollback`. Dead children stay dead until restarted.
Sumika has no Project type, no policy, and no credentials, and it never
logs PTY contents.

Both products call their main object a **Session**, but they mean
different things. A rusui Session is durable work on a project and an
environment. A Sumika Session is one live process. Joining the products
without separating those identities would make PTY lifecycle, leases,
terminal attach, and credentials compete for one state model.

## Decision

Split ownership the way a control plane and a node agent do. rusui is the
control plane: it records intent, policy, and history, and it is the only
durable store. Sumika is the local node agent: it owns live processes and
reports what it observes. Neither copies the other's authoritative state.

### D1. Authority

| Object | Authority | Other side holds |
| --- | --- | --- |
| **Project** | rusui | nothing; Sumika has no project (its ADR 0003) |
| **Session** (durable work) | rusui | the Process name only |
| **Environment** | rusui | nothing; a local environment is the operator's host |
| **Process** (live PTY child; Sumika calls it a Session) | Sumika | rusui records the name and last observed status as events |
| **Turn** (leased attempt) | rusui, unattended sessions only | not used for local sessions |
| **Attach** (exclusive terminal client) | Sumika for local processes; rusui for managed environments | neither proxies the other |
| **Status report** (`idle` / `blocked` / `running`) | Sumika observes | rusui stores received reports as events, never as truth |
| **Terminal bytes** | Sumika, in memory | rusui never stores or relays local PTY bytes |
| **Receipts** (policy decisions, actions) | rusui | nothing |

In rusui's vocabulary a Sumika Session is a **Process**. rusui never uses
"session" for it.

### D2. A new `local` session kind

Local interactive work uses a new session kind, `local`, not `run`. `run`
means unattended: leased turns, lease budgets, plane-brokered grants, and,
for pinned tasks, the ADR 0015 GitHub credential. A human at a local
terminal needs none of those, and reusing `run` would give them all to it.

Policy consequences:

- A project must list `local` in `session_kinds`. It is not in the
  default set.
- `local` sessions take no turns, count against no concurrent-lease
  meter, and receive no per-turn grant, model proxy, or GitHub
  credential. They are never implement sessions.
- Pause stops new `local` sessions. It does not kill running processes;
  cancel does (D3).

### D3. Lifecycle mapping

| Event | Sumika | rusui |
| --- | --- | --- |
| start | `start {name, argv, cwd}` | creates the `local` session first, then asks Sumika to start the Process |
| running / idle / blocked | hook `report` | records a status event with observed time |
| detach | client leaves; child stays | no change |
| reconnect | `attach` | no change |
| steal | new attach displaces old | no change; attach is Sumika's |
| process death | status `dead`; no respawn | records `process_exited`; the session stays open for an explicit restart |
| restart | `kill` then `start` after `dead` | operator action on the session; same Process name |
| cancel | `kill` | session `cancelled`, then `kill` |
| environment replacement | not applicable | not applicable to `local`; unchanged for managed environments |
| sleep / wake | not applicable | not applicable to `local` |
| expiry | none | none by default; an operator may close the session, which kills the Process |

### D4. Names, collisions, and stale processes

- The Process name is `rusui-<project>-<session id>`. Session ids are
  never reused, so names are unique per plane.
- If `start` finds a live Process with that name but a different argv or
  cwd, rusui records a collision and does not attach the session to it.
- On plane start and on a timer, rusui reconciles by calling `list`:
  - a `rusui-` Process with no open `local` session is reported as an
    **orphan**. rusui does not kill it, because it may hold a human's
    unsaved work; the operator decides.
  - an open `local` session whose Process is missing is marked
    `process_lost` and can be restarted.
- Processes without the `rusui-` prefix belong to the operator and are
  ignored.

### D5. Protocol boundaries

- **Local profile.** The plane talks to the Sumika daemon over Sumika's
  existing socket protocol, only when both run as the same user on the
  same host. rusui adds no new wire protocol and no second store. Sumika
  keeps its in-memory process table and its own config; rusui keeps
  sessions, events, and receipts.
- **Managed profile.** Unchanged. Runners, containers, the terminal write
  lease, and the ACP editor work as today. Sumika is not used inside
  managed environments.

### D6. Credentials

Sumika never becomes a policy, credential, or remote-auth plane. A local
Process inherits the operator's own shell environment, because it runs on
the operator's machine as the operator. rusui injects no grant, token, or
proxy into it and does not treat that environment as brokered. The ADR
0015 GitHub credential is only for implement sessions, which are never
`local`.

### D7. Existing surfaces

The managed terminal ([ADR 0012](0012-operator-access.md), #114) and the
ACP editor shim (#116) apply to managed environments only and are
unchanged. The console may later list `local` sessions and their last
reported status read-only. It does not attach to local PTYs; that is
`sumika attach`.

### D8. Scope

- **Core:** none yet. The combined runtime is not part of the stable
  core until the supported-core decision (#141) includes it.
- **Experimental:** the local profile, meaning the `local` session kind,
  Sumika start/kill/list over the same-uid socket, status events, and
  orphan reporting.
- **Deferred:** Sumika inside managed environments, remote Sumika
  daemons, pairing or TLS transport, shared or multiplayer attach, console
  attach to local PTYs, and automatic orphan cleanup.

## Consequences

Easier: each product keeps one state model. Sumika stays a process
supervisor with no policy; rusui stays the only durable store. Status
works the same way in both, from hooks or `unknown`, never scraped.

Harder: rusui gains a second runtime profile and a reconcile loop against
an external daemon. `local` status is only as fresh as the last hook
report.

Schema impact when implemented: a new session kind value and status
events; no change to turns, leases, or grants. Policy impact: `local`
must be named in `session_kinds`. Runner contract: unchanged.

## Rejected alternatives

- **Reuse the `run` kind for local work.** Applies unattended leases,
  budgets, and credentials to a human-driven process.
- **Make Sumika a rusui runner.** Turns a process supervisor into a
  policy and lease participant and needs a second authoritative store.
- **Mirror PTY bytes into rusui.** Duplicates terminal state, contradicts
  Sumika's no-content logging, and adds a large new data class to SQLite.
- **Give Sumika a Project type.** Rejected by Sumika's own ADR 0003;
  Project stays rusui's.
- **Kill orphan processes automatically.** May destroy a human's work in
  progress; report instead.

## Validation and reversal

Validation: a local-profile spike starts a `local` session and sees the
Process in `sumika list`, receives a `blocked` report as an event, cancels
the session and sees the Process killed, and reports an orphan after the
session is closed out of band. Reverse by superseding this ADR; no
accepted contract depends on it yet.

## Sources

- [#182](https://github.com/Sannrox/rusui/issues/182)
- Sumika `CONTEXT.md`, `VISION.md`, `docs/decisions/0001-week0.md`,
  `docs/decisions/0003-cwd-is-not-a-project.md`, and
  `crates/sumika-protocol/src/lib.rs` at
  [`85c50ed`](https://github.com/Sannrox/sumika/commit/85c50ed)
- rusui [CONTEXT.md](../../CONTEXT.md), [ADR 0011](0011-unattended-session-contract.md),
  [ADR 0012](0012-operator-access.md), [ADR 0015](0015-agent-publication.md)

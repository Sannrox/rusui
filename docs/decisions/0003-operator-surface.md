# ADR 0003: Operator surfaces are views of one object API

- Status: Accepted
- Date: 2026-09-14
- Amends: [ADR 0001](0001-environment-plane.md) D6 and D7 (concrete
  stacks and reachability). The API-first and Slack-as-adapter
  decisions stand.
- Resolves: [#7](https://github.com/Sannrox/rusui/issues/7)
- Discussion: none. Merging with this status is the acceptance act.

## Context

The plane now has typed records (environment, session, turn, runner,
event, action) and an outbound runner. Humans still reach it only
through Slack slash commands and logs, on loopback. A single maintainer
cannot fund a second product. The surfaces must be viewers of the same
objects, not parallel state machines.

ADR 0002 already proved Grok speaks ACP over stdio as a guest agent.
A throwaway local shim (kept out of this repository) proved the other
direction: an ACP client can attach to a rusui-shaped agent facade,
receive streamed transcript updates, and stop at `end_turn`. A real
editor attach is P1.4.

## Decision

Every surface reads and writes the same object types through one API.
No surface owns a private copy of session or approval state.

1. **HTTP + SSE is the system API.** gRPC is reserved for family
   services (onmyoji, sekai-chisei). New operator features land on HTTP
   first.
2. **`rusui` CLI is the primary operator tool.** `run`, `sessions`,
   `attach`, `logs`, `approve`, `sync`. Scriptable; same objects as the
   API.
3. **ACP facade.** rusui exposes a session as an ACP agent. A local
   shim (stdio) forwards to the plane. Editors and onmyoji render
   transcript, tool calls, diffs, and `session/request_permission`.
   rusui is not a chat app.
4. **Embedded console** for what an editor cannot show. Stack: Go
   `html/template` + htmx + SSE in the rusui binary. xterm.js is the
   only allowed JS island (terminal). No SPA toolchain, no separate
   Node build.
5. **Chat (Slack first)** carries notify and approve only. No
   transcript dump, no "talk to the agent" channel.
6. **Reachability.** Bind may be non-loopback. Authentication is a
   local bearer token at first, then an identity-aware proxy or tailnet
   in front of the same token check. OIDC is later. Secrets stay
   required (#3).
7. **onmyoji** dispatches to a rusui environment/session, not a local
   worktree. That change lives in onmyoji; rusui only exposes the API.

### Console stack comparison

| Need | Go templates + htmx + SSE | SPA (React/Vue) |
| --- | --- | --- |
| Streaming transcript | SSE into HTML fragments; one binary | Extra client, two deploys |
| xterm terminal | One JS island | Easy, but pulls in a bundler |
| Diff view | Server-rendered `<pre>` / patch | Richer UX, more surface |
| Maintainer cost | Fits the existing Go tree | A second product |

P1 ships the left column.

### P1 console views (ten)

1. Sign-in (token)
2. Sessions
3. Session detail (transcript + status)
4. Approvals inbox
5. Environments
6. Runners
7. Actions / receipts
8. Terminal
9. Budgets
10. Policy / health

No more screens in P1. Portals wait for P2.

## Open questions

- A live editor (Zed or similar) attaching to a remote rusui session
  (P1.4). The off-repo probe used a protocol-shaped client, not a GUI.
- Exact tailnet product. Any identity-aware proxy that can inject or
  require the bearer token is enough.

## Prototype evidence (kept out of the repository)

2026-09-14, `/tmp/rusui-acp-facade-probe/probe.py`. An editor-shaped ACP
client attached over stdio to a rusui-shaped facade that only views a
typed session object. Observed: `initialize` named `rusui-facade-probe`,
`session/new` returned `sess_env1_turn1`, one `session/update`
`agent_message_chunk` (`pong from environment session sess_env1_turn1`),
`session/prompt` completed with `stopReason=end_turn`. No model loop
inside the facade.

## Consequences

- P1.4 implements the API, CLI, ACP shim, and the ten views above.
- Slack stays an adapter. A chat-first UI is a rejected product.
- Console CSS may be ugly; that is not a follow-up issue.

## Rejected alternatives

- **Chat-first console.** Duplicates the agent transcript and fights
  ACP editors.
- **SPA as the P1 console.** Two build graphs for one operator.
- **Desktop or mobile app in rusui.** onmyoji is the desktop client.
- **Keep loopback-only.** Approvals from a phone would require a
  tunnel that is not an auth model.

## Validation and reversal

Accept on merge. Reopen if the ten-view list grows before P1.4 ships,
or if SSE cannot carry the transcript without a websocket.

## Sources

- [ADR 0001](0001-environment-plane.md) D6, D7
- [ADR 0002](0002-grok-acp-agent-set.md) (ACP stdio evidence)
- [#7](https://github.com/Sannrox/rusui/issues/7)

# ADR 0002: P1 agent set is Grok CLI over ACP

- Status: Accepted
- Date: 2026-09-14
- Amends: [ADR 0001](0001-environment-plane.md) D2 (chosen agent set and
  how tool calls become receipts). D2's "ACP is the only agent interface"
  stands.
- Resolves: [#6](https://github.com/Sannrox/rusui/issues/6)
- Unblocks: [#10](https://github.com/Sannrox/rusui/issues/10)
- Discussion: none. GitHub Discussions are disabled; the pull request that
  adds this file is the review venue. Merging with status Accepted is the
  acceptance act.

## Context

ADR 0001 D2 made ACP the only agent interface and treated headless viability
of Codex, Claude Code, and Gemini CLI as a Phase 0 gate. Local flag
inspection on 2026-09-14 showed that Codex `0.153.4` and Claude Code
`2.1.79` expose first-party headless CLIs (`codex exec`, `claude -p`) and
MCP, but no ACP server. Gemini CLI was not inspected. Cursor CLI documents
`agent acp`; the operator declined to install it.

The operator then named the supported set: Grok CLI only. Grok `0.2.112`
is already installed as `~/.local/bin/agent` (do not confuse this path
with Cursor's documented `agent` binary).

## Decision

- **P1 supported agent set:** Grok CLI (`grok` / `agent` `0.2.112` or
  later that still speaks ACP). Spawn:

  ```text
  agent --permission-mode default agent stdio
  ```

- **No Codex, Claude Code, Gemini, or Cursor adapters** in P1. First-party
  `exec`/`-p` wrappers are out of scope. Cursor ACP remains unused until
  the operator installs it and a later ADR adds it.
- **`internal/acp` (#10)** targets this spawn only. Conformance is
  `initialize`, `session/new` or `session/load`, `session/prompt`,
  `session/update`, and `session/request_permission`.
- **shikigami** still needs an ACP server before it is a P1 guest. Until
  then its `serve` intake stays the interim family path (ROADMAP A5).
- **Receipts.** Grok executes tools inside the agent process. `fs/*` and
  `terminal/*` did not appear as client methods in the probe, even when
  the client advertised no filesystem or terminal capability. The plane
  therefore cannot assume D2's "every tool call is a client-side ACP
  method." Isolate the machine; record `session/update` tool calls; map
  `session/request_permission` to policy when it fires. `--always-approve`
  (and the default stdio permission path) skips that mapping and is not
  the unattended production spawn.

## Observed matrix (2026-09-14)

Probe transcripts stay off-repo. Same prompt shape: JSON-RPC 2.0,
newline-delimited, stdio.

| Agent | ACP spawn | `initialize` / `session/new` / `session/prompt` | `session/request_permission` | `fs/*` / `terminal/*` via client | Auth seen |
| --- | --- | --- | --- | --- | --- |
| Grok `0.2.112` | `agent --permission-mode default agent stdio` | Yes. Prompt `stopReason=end_turn`; streamed `pong` | Yes for workspace shell. Options `allow-once`, `reject-once`. Answering `allow-once` completed the turn and wrote the file | Not observed. Agent ran `run_terminal_command` itself | `cached_token` (`~/.grok/auth.json`), `grok.com`. `agent login --oauth` / `--device-auth`. API-key auth is not disabled |
| Grok `0.2.112` without `--permission-mode` | `agent agent stdio` | Yes | Did not fire for cwd write or shell | Not observed | Same |
| Codex `0.153.4` | none | n/a | n/a | n/a | ChatGPT login; `--with-api-key`, `--device-auth` |
| Claude Code `2.1.79` | none | n/a | n/a | n/a | `claude auth login --claudeai` / `--console` |
| Gemini CLI | not installed; operator said ignore | — | — | — | — |
| Cursor agent | not installed; operator declined | Documented `agent acp` only | — | — | — |

Grok also has `-p` / `--single`, `--output-format`, `--always-approve`,
`agent agent serve` (loopback WebSocket), and `agent mcp`. Those are not
the P1 rusui interface.

## Open questions

- 24-hour soak and rate/cost limits (belongs to #10, not this decision).
- Whether Grok ever offers `allow-always`, or ever issues client-side
  `fs/*` / `terminal/*`.
- xAI terms for a third-party supervisor speaking ACP (not read).
- Gemini CLI and Cursor ACP remain unmeasured.

## Consequences

- Phase 0 no longer waits on three vendor ACPs. #10 is unblocked once
  this ADR is accepted.
- Positioning stays "ACP guest on metal you control"; the first guest is
  Grok, not "any popular coding agent."
- D2's tool-ownership story is environment isolation plus ACP
  notifications and permission RPCs, not a Grok-side prohibition on
  running tools.

## Rejected alternatives

- **Keep A1 as Codex + Claude + Gemini.** Those CLIs do not speak ACP
  here; adapters would be first-party exec wrappers, which ADR 0001
  rejected.
- **Install Cursor CLI and make it the first guest.** Operator declined.
- **Grok `-p` / `--always-approve` only.** Works unattended but skips
  `session/request_permission`, so policy cannot sit on the ACP
  permission channel.
- **Declare D2 failed and embed a model loop.** Unnecessary; Grok ACP
  completed a headless prompt under the operator's cached login.

## Validation and reversal

Accept when this file merges. Reopen if Grok drops stdio ACP, if
`--permission-mode default` stops emitting `session/request_permission`
for shell, or if the operator adds another agent to the supported set.

## Sources

- Local binaries on 2026-09-14: Grok `0.2.112` (`9bbd559437aa`), Codex
  `0.153.4`, Claude Code `2.1.79`, Cursor editor `3.20.10`.
- Off-repo probe: `initialize` + `session/new` + `session/prompt` ("pong");
  shell write with and without `--permission-mode default`.
- Grok user guide (agent mode / ACP stdio): `grok agent --always-approve
  stdio`.
- Cursor CLI ACP docs (unread against a local binary):
  https://cursor.com/docs/cli/acp
- [ADR 0001](0001-environment-plane.md), [#6](https://github.com/Sannrox/rusui/issues/6)

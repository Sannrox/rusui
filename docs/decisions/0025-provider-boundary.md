# ADR 0025: One Go provider boundary for Grok, Claude, and Codex

- Status: Accepted; amended by [ADR 0073](0073-session-plane-guest-registry.md)
- Date: 2026-09-27
- Amends: [ADR 0002](0002-grok-acp-agent-set.md) (the supported agent set)
  and [ADR 0017](0017-claude-guest-and-model-upstream.md) D1 (Claude is no
  longer the npm ACP adapter).
- Resolves: [#283](https://github.com/Sannrox/rusui/issues/283)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md). This ADR rewrites the
  guest-spawn sentence there. The per-turn grant is unchanged.
- Discussion: none. Merging with this status is the acceptance act.

## Context

Grok is a Go ACP guest. Claude was a pinned npm package,
`claude-agent-acp`, on the guest image. Codex was not a guest. Each new
provider would otherwise grow its own spawn path, credential layout, and
client. The plane already has one Session, one Turn, and one per-turn grant.

## Decision

Grok, Claude, and Codex run through one Go boundary in this repository,
`internal/provider`. A provider is supported only when it passes that
package's conformance suite. The CLI, the console, and the editor do not
branch on the provider. They read the same session events.

`claude-agent-acp` is not the Claude guest. It is not installed on the
guest image and it is not spawned.

The Rusui Session outlives the provider process. Resume starts a new
process against the cursor stored for that Session. It does not import a
conversation from the provider's home directory. The Turn ends when the
provider turn ends.

The provider adapters accept a cursor, and the conformance suite proves
resume for all three pins. The live runner does not use it on every host
yet:

- The Grok host loads the stored cursor with `session/load`. When the load
  fails, it starts a new session.
- The Claude host records the cursor from the `init` event but does not
  pass it. It spawns without `--resume`, so each Claude turn is a new
  provider conversation. Continuity comes from the prompt the plane sends.
  Claude resume is deferred until the runner can start a new conversation
  when the stored transcript is gone.
- The Claude host does not forward the provider's events. The session
  transcript comes from plane receipts.

### Pinned protocols

| Provider | Process | Protocol | Pin |
| --- | --- | --- | --- |
| Grok | `agent --permission-mode default agent stdio` | ACP JSON-RPC | `protocolVersion` 1 |
| Claude | `claude --print --input-format stream-json --output-format stream-json --verbose --permission-mode default --permission-prompt-tool stdio` | Claude Code stream-json | CLI `@anthropic-ai/claude-code` 2.1.283 (`claude_code_version` in `system/init`) |
| Codex | `codex app-server --listen stdio://` | app-server JSON-RPC, experimental | `app-server-2026-04-15` |

An answer that names another version is refused before the turn starts.
Amended 2026-09-29 ([#396](https://github.com/Sannrox/rusui/issues/396)),
from the real 2.1.283 binary: Claude emits `system/init` only after the
first user message, so the prompt is written first and `init` (version,
resume cursor) is checked before any other event is acted on. Its `init`
names no protocol field; the argv fixes the stream format. Permission
prompts reach rusui only with `--permission-prompt-tool stdio`, as
`control_request` `can_use_tool`; the adapter maps the tool to an ACP kind
for the policy gate and answers with a `control_response` allow or deny.
Codex drift is a refusal, not a best-effort parse.

### Instances

An instance is one account and one configuration of one provider kind.
Rusui creates its directory. Two instances do not share mutable session
state, model catalog, or credentials. `HOME` set to another directory is
refused. A directory marked as copied from another home is refused.
Claude uses `CLAUDE_CONFIG_DIR`. Codex uses `CODEX_HOME`. Neither
directory is the operator's login.

A probe is a version check. It does not start a Session and it does not
open a login browser.

A deny is a deny. If the provider's option list omits a reject id, the
adapter still refuses. It does not answer allow.

None of these pinned protocols can rewind a conversation. Revert is
refused before a file change. Attachments stay outside the workspace.

## Consequences

- The reference guest image installs Claude Code CLI 2.1.283 and does not
  install `claude-agent-acp`.
- `RUSUI_GUEST` accepts `grok`, `claude`, and `codex`.
- Codex uses the OpenAI bearer path on the existing model proxy. The
  per-turn grant is unchanged.
- Instance directories are plane-owned and hold no provider secret.

## Rejected alternatives

- **Keep `claude-agent-acp` as the Claude guest.** That is an adapter
  maintained outside this repository. The issue requires the provider's
  own CLI.
- **One spawn path per provider in the CLI and console.** Those surfaces
  would branch on the provider. They keep reading the session.
- **Share one provider home across accounts.** A second account would see
  the first account's credentials.

## Validation and reversal

The conformance suite runs the same scenarios for all three pins: start,
prompt, transcript, tool call, permission allow request that must still
deny, process kill, and resume without a duplicate. It also covers two
instances, a version probe, the Claude usage-limit report, revert, an
attachment outside the workspace, and a Codex question that survives
process exit. Reverse by superseding this ADR.

## Sources

- [#283](https://github.com/Sannrox/rusui/issues/283)
- [ADR 0002](0002-grok-acp-agent-set.md)
- [ADR 0017](0017-claude-guest-and-model-upstream.md)
- Claude Code stream-json: `claude --print --input-format stream-json --output-format stream-json`
- Codex app-server stdio: `codex app-server --listen stdio://`

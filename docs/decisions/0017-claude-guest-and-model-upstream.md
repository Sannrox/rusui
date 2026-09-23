# ADR 0017: Claude Code guest, operator model upstream, and a fenced self-test

- Status: Accepted
- Date: 2026-09-23
- Amends: [ADR 0002](0002-grok-acp-agent-set.md) (supported agent set),
  [ADR 0009](0009-credential-broker.md) (model proxy upstream; the guest
  still holds only the per-turn grant),
  [ADR 0014](0014-pilot-evaluation-deferred.md) precondition 3 (names the
  pilot repository and guest).
- Resolves: [#119](https://github.com/Sannrox/rusui/issues/119) by
  maintainer selection. Its dependency on the pilot (#103) is waived: the
  pilot cannot run while the only supported guest is unavailable.
- Related: [ADR 0015](0015-agent-publication.md) (implement sessions),
  [#181](https://github.com/Sannrox/rusui/issues/181),
  [#103](https://github.com/Sannrox/rusui/issues/103).
- Discussion: none. GitHub Discussions are disabled. The maintainer chose
  this direction directly; merging with this status is the acceptance act.

## Context

ADR 0002 limited P1 to Grok because, on 2026-09-14, Claude Code (2.1.79)
and Codex had no ACP server. Grok is now unavailable for the self-test: its
usage balance is exhausted. Claude Code (2.1.280) still has no built-in ACP
server, but the ACP project publishes an adapter,
`@agentclientprotocol/claude-agent-acp` (0.81.1), built on the official
Claude Agent SDK. Editors use it to run Claude Code over ACP.

ADR 0009 keeps the real model credential on the plane: the guest calls the
plane model proxy with its per-turn grant, and the proxy swaps in the key.
The proxy only speaks to `https://api.x.ai` with a bearer key today.
Operators may hold an API key, or only a subscription login used through
a local CLI proxy that exposes it as a provider-compatible API.

ADR 0015 lets implement sessions publish with the operator's credential.
On a repository with one maintainer, GitHub cannot tell the agent from
the operator: merging needs the same `contents: write` permission as
pushing, and a solo maintainer cannot require someone else's approval.
Coding agents that publish as the user, such as Amp, handle this with
command-level permission rules (allow, ask, reject) and say plainly that
those rules are workflow control, not a security boundary.

## Decision

### D1. Two supported guests

- **Grok** (default): the ADR 0002 spawn, unchanged.
- **Claude Code**: the ACP adapter `claude-agent-acp` at a pinned version,
  spawned over stdio like Grok. The guest runs with an isolated
  `CLAUDE_CONFIG_DIR` and never sees the operator's Claude login.

The plane setting `RUSUI_GUEST` (`grok` or `claude`) selects the guest.
Claim responses name it, and the runner spawns it. Conformance for either
guest is the ADR 0002 set: `initialize`, `session/new` or `session/load`,
`session/prompt`, `session/update`, and `session/request_permission`.

### D2. Operator-configured model upstream

The model proxy has a **provider**, chosen by the guest: `xai` for Grok and
`anthropic` for Claude Code. Each provider has a default upstream
(`https://api.x.ai`, `https://api.anthropic.com`) and its own
authentication header (bearer for xAI, `x-api-key` for Anthropic).

- `RUSUI_MODEL_UPSTREAM` replaces the default upstream URL. That URL can be
  a gateway or a **CLI proxy**: a service on the plane host that uses the
  operator's own CLI login and speaks the provider's API.
- The provider key (`XAI_API_KEY` / `RUSUI_XAI_API_KEY`, or
  `ANTHROPIC_API_KEY` / `RUSUI_ANTHROPIC_API_KEY`) is sent upstream when
  set. It may be empty only when `RUSUI_MODEL_UPSTREAM` is set.
- With neither a key nor an upstream, the proxy fails closed, as today.
- The guest receives only the per-turn grant, as the base URL's credential
  (`ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN` for Claude Code). No
  model key, subscription token, or CLI login enters the guest.

Whether a subscription may be used through a CLI proxy for automated work
is governed by the provider's terms. The operator decides that; rusui does
not.

### D3. Tool-fence reject rules

Project policy `permissions` gains reject rules, checked before allow
rules. A matching reject answers the permission request with a rejection
the agent can read, like Amp's `reject`. rusui applies a built-in reject
set to implement sessions whether or not policy names it:

- `gh pr merge`, `gh pr close`, `gh pr review`, `gh pr ready --undo`
- `gh release`, `gh repo`, `gh workflow`, `gh secret`, `gh variable`,
  `gh ruleset`
- `gh api` with `-X`, `--method`, `-f`, `-F`, `--field`, or `--input`
- `git push` naming the default branch, `--force`, `-f`, `--delete`, or
  `--mirror`

These rules are workflow control, not a security boundary. They see only
permission requests the guest makes, and a script can hide a command from
substring matching. Branch protection is what makes mistakes recoverable.

### D4. Solo repository profile

The #193 protection profile required approval from someone other than the
last pusher. A solo maintainer cannot meet that. The supported solo
profile is: pull requests required, required status checks, conversation
resolution, and no force pushes or deletion on the default branch. It
does not stop the agent from merging with the operator's token. D3 is the
guard for that, and both documents say so.

### D5. The self-test runs on rusui

The pilot repository is `Sannrox/rusui` itself, and the guest is Claude
Code (D1). This satisfies ADR 0014 precondition 3. The #181 end-to-end run
and the #103 cohort both use it. rusui opens pull requests on itself as
the maintainer, and the maintainer merges them.

## Consequences

Easier: the pilot no longer depends on one vendor's balance. Operators can
use whichever credential they already hold without putting it in the
guest.

Harder: two guests to keep conformant, and an npm-distributed adapter
whose version must be pinned in the guest image. D3 adds a rule set that
must be kept current as `gh` grows commands.

Security: unchanged for model credentials (they stay on the plane).
Implement sessions keep ADR 0015's trade-off; D3 narrows ordinary
mistakes and does not remove it.

## Rejected alternatives

- **Put the operator's Claude login in the guest.** Same as copying
  `~/.grok/auth.json`, which ADR 0009 rejected.
- **Wait for Grok credits.** Leaves the pilot blocked on a single vendor.
- **Require a second GitHub identity for the self-test.** ADR 0015
  rejected a bot account; revisit only if D3 proves insufficient.
- **Treat the reject rules as a boundary.** They are not, and claiming it
  would mislead operators.

## Validation and reversal

Validation: a Claude Code implement session on rusui opens one pull
request through the plane model proxy, with trailers, and a guest attempt
to run `gh pr merge` is rejected by the fence. Reverse by superseding this
ADR; Grok remains the default guest.

## Sources

- [ADR 0002](0002-grok-acp-agent-set.md), [ADR 0009](0009-credential-broker.md),
  [ADR 0014](0014-pilot-evaluation-deferred.md), [ADR 0015](0015-agent-publication.md)
- `@agentclientprotocol/claude-agent-acp` 0.81.1 on npm
  ([repository](https://github.com/agentclientprotocol/claude-agent-acp))
- [How We Think about Permissions – Amp](https://ampcode.com/notes/permissions)
- `Sannrox/rusui` default-branch protection, read 2026-09-23

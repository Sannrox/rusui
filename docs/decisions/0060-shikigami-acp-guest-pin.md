# ADR 0060: Pin shikigami acp as a supported guest

- Status: Accepted; amended by [ADR 0073](0073-session-plane-guest-registry.md)
- Date: 2026-10-06
- Amends: [ADR 0002](0002-grok-acp-agent-set.md) (shikigami needed an
  ACP server before it was a P1 guest).
- Resolves: [#494](https://github.com/Sannrox/rusui/issues/494)
- Related: [ADR 0017](0017-claude-guest-and-model-upstream.md),
  [#507](https://github.com/Sannrox/rusui/issues/507) (spawn
  implementation), [#120](https://github.com/Sannrox/rusui/issues/120)
  (second-guest track; Claude occupies it).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

ADR 0002 pins Grok. ADR 0017 pins Claude. Shikigami now has
`shikigami acp` (shikigami ADR 0014). Rusui does not spawn it. #120 is
the optional second-guest track and is the wrong slot: Claude already
occupies it.

[#494](https://github.com/Sannrox/rusui/issues/494) asked what argv,
permission mode, and session methods make `shikigami acp` a supported
guest beside Grok and Claude. Two options:

1. Pin a shikigami spawn the way Grok and Claude are pinned.
2. Leave shikigami on its `serve` path and do not add a guest kind.

Public shikigami docs name the spawn, the permission path, and the
session methods. ADR 0002's gate ("needs an ACP server") is met.
This ADR records the pin. It does not spawn the guest.

Source evidence at the commit this decision was made against:

- Shikigami `docs/acp.md`: `shikigami --state ./state acp` over
  stdio, newline-delimited JSON-RPC.
- Shikigami ADR 0014: thin process host; ask=park via
  `session/request_permission`; always-approve rejected.
- Methods: `initialize`, `session/new`, `session/load` (fail closed),
  `session/prompt`, `session/update`, `session/request_permission`,
  `session/cancel`.
- Rusui `SpawnArgsFor` still fails closed on an unknown guest,
  including `shikigami`. `RUSUI_GUEST` still wants grok, claude, or
  codex.

## Decision

**D1. Pin the shikigami guest.** The supported spawn is:

```text
shikigami --state <plane-chosen-state-dir> acp
```

The plane chooses the state directory inside the environment.
Credentials come from the environment (per-turn grant). There is no
ACP login.

**D2. Permission is `session/request_permission`.** Ungoverned
mutating tools ask=park. `--always-approve` is not the spawn.

**D3. Session methods** are the ADR 0002 set plus `session/cancel`
and fail-closed `session/load`.

**D4. This issue does not spawn.** `RUSUI_GUEST=shikigami` stays
refused until [#507](https://github.com/Sannrox/rusui/issues/507)
implements the pin. #120 stays the Claude second-guest track.

## Consequences

- Easier: one named spawn for the first-party agent.
- Harder: the guest image and `RUSUI_GUEST` still omit shikigami
  until #507.
- Schema, policy, runner contract, and public API are unchanged in
  this change. #507 will amend spawn.

## Rejected alternatives

- **Leave shikigami on `serve` (option 2).** Weaker receipts. Rusui's
  only agent interface is ACP. The ACP server now exists.

## Validation and reversal

Accept on merge. Validated when this ADR names the argv, and
`SpawnArgsFor("shikigami")` still errors until #507.

Reverse with a superseding ADR that changes the argv or drops the
guest kind.

## Sources

- [#494](https://github.com/Sannrox/rusui/issues/494)
- [ADR 0002](0002-grok-acp-agent-set.md)
- Shikigami ADR 0014 and `docs/acp.md`
- Decided against `main` at `c4d0945`.

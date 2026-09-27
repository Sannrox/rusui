# ADR 0026: Harness, model, and upstream are independent

- Status: Accepted
- Date: 2026-09-27
- Amends: [ADR 0017](0017-claude-guest-and-model-upstream.md) D1–D2. The
  guest selects the harness and the proxy protocol. It does not imply a
  model id.
- Resolves: [#295](https://github.com/Sannrox/rusui/issues/295)
- Related: [ADR 0025](0025-provider-boundary.md) names the harness
  binaries. Diagnose of the named model is
  [#293](https://github.com/Sannrox/rusui/issues/293).
- Discussion: none. Merging with this status is the acceptance act.

## Context

A Claude harness speaks Anthropic Messages. The plane already points that
process at the model proxy and gives it only the per-turn grant. The
upstream behind the proxy can be the provider API or an
Anthropic-compatible gateway. The model id is the string in the request
body. Treating the harness as if it chose that id left operators with no
supported way to name the model the turn will call. A catalog
`GET /v1/models` can succeed while a bounded prompt to the configured id
returns HTTP 429.

## Decision

Three settings, set independently:

| Object | Setting | Effect |
| --- | --- | --- |
| Harness | `RUSUI_GUEST`, claim `guest`, `acp.SpawnArgsFor` | which process runs, and which proxy protocol the grant uses |
| Upstream | plane model proxy and `RUSUI_MODEL_UPSTREAM` | where the grant is swapped and the body is forwarded |
| Model | `RUSUI_GUEST_MODEL`, claim `guest_model` | the id the guest puts in the body |

`DriverEnv` copies the model id. It does not contain a compiled model id.
For the Claude harness, a valid id is copied to `ANTHROPIC_MODEL` and to
`ANTHROPIC_DEFAULT_OPUS_MODEL`, `ANTHROPIC_DEFAULT_SONNET_MODEL`, and
`ANTHROPIC_DEFAULT_HAIKU_MODEL`. Those tier variables keep compaction and
subagents on the same id. The Claude guest also receives an empty
`ANTHROPIC_API_KEY` and the fresh `CLAUDE_CONFIG_DIR`, so a cached login
does not beat the per-turn grant.

The Grok harness stays the Grok argv and the xAI grant
(`XAI_API_KEY`, `GROK_XAI_API_BASE_URL`). It does not receive the
Anthropic model variables. Switching harness is a different operation
from naming a model.

`rusui diagnose` reports `model_upstream` from `GET /v1/models` and
`model_guest` from one bounded prompt to the configured id. A 429 on that
prompt is `unavailable`. A ready catalog is not proof the guest can prompt.
Model keys stay on the plane.

## Consequences

Operators can name a gateway model on the Claude harness without changing
the guest binary. Unset model for Claude stays misconfigured. A later
change of model during a live turn is out of scope; the id is fixed at
spawn.

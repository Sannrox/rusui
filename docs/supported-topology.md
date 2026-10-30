# Supported topology

> **Explanation.** This page describes the supported unattended profile and
> the limits of its evidence. Follow the [operator guide](operator.md) to
> configure and run it. The 1.0 freeze is
> [ADR 0086](decisions/0086-supported-1.0-core-contract.md).

## Supported 1.0 version tuple

`rusui` and `rusui-runner` from the same git commit, one runner on the
plane host, Docker or Podman CLI, digest-pinned `$RUSUI_GUEST_IMAGE`,
default guest shikigami ACP. Mixing commits is unsupported; release
binaries refuse a runner hello from a different commit. Schema, policy,
restore, and export versions: [compatibility.md](compatibility.md).

| Piece | 1.0 value |
| --- | --- |
| Binaries | `rusui` + `rusui-runner`, same commit (git tag via `scripts/lib/version.sh`) |
| Host OS | Linux or macOS |
| Runner | one `rusui-runner` on the plane host ([ADR 0029](decisions/0029-single-host-runner.md)) |
| Runtime | Docker or Podman CLI (`docker`/`podman` on `PATH`) |
| Default guest | shikigami ACP from the reference image of that commit ([ADR 0073](decisions/0073-session-plane-guest-registry.md)) |
| Named guests | `shikigami`, `grok`, `claude` (Claude Code CLI 2.1.283), `codex` |
| Generic ACP | plane-signed conformance receipt ([ADR 0082](decisions/0082-signed-conformance-receipts.md)) |
| Image | digest-pinned `$RUSUI_GUEST_IMAGE`; toolchain baked in ([ADR 0050](decisions/0050-guest-toolchain-in-image.md)) |
| Egress | `trusted`: HTTPS to `rusui.plane` only |
| Listen | loopback `127.0.0.1` |
| Store | one SQLite file, WAL, one writer |

The reference guest image is a separate digest-pinned artifact, not a
floating tag and not a per-session download. Docker Desktop and Podman
Desktop are not this tuple
([ADR 0062](decisions/0062-guest-link-desktop-podman-deferred.md)).

## Agent and runtime matrix

| Harness | Protocol | 1.0 class | Notes |
| --- | --- | --- | --- |
| shikigami | ACP | supported (default) | Baked in the reference image |
| grok | ACP | supported | Named built-in |
| claude | Claude Code stream-json 2.1.283 | supported | Baked in the reference image |
| codex | app-server | supported | Named built-in |
| generic ACP | ACP subset | supported with signed receipt | [ADR 0082](decisions/0082-signed-conformance-receipts.md) |
| `claude-agent-acp` | — | excluded | Not spawned |
| Sumika local | PTY | experimental / excluded from unattended 1.0 | [ADR 0045](decisions/0045-local-unattended-promotion-deferred.md) |
| process driver | host process | test/dev only | Not public-repo unattended |
| Firecracker / microVM | — | excluded | [ADR 0028](decisions/0028-container-isolation-profile.md) |

Other OS/runtime/image combinations are not claimed.

`rusui diagnose` and `GET /readyz` report topology checks as `ready`,
`misconfigured`, or `unavailable`; `rusui diagnose` also probes the model
upstream. Neither prints secret values.
Exit status 0 means the topology is ready; do not start unattended work
otherwise. The `model_upstream` check sends a bounded `GET /v1/models` request
using the configured provider credential or upstream authentication and does
not generate a model response. That list is not proof the guest can prompt.
The harness, the model, and the upstream are three settings.
`RUSUI_GUEST` selects the harness process. `RUSUI_MODEL_UPSTREAM` selects
where the plane model proxy forwards the body. `RUSUI_GUEST_MODEL` is the
id that process puts in the body. For Claude, the plane also copies that
id into `ANTHROPIC_DEFAULT_OPUS_MODEL`, `ANTHROPIC_DEFAULT_SONNET_MODEL`,
and `ANTHROPIC_DEFAULT_HAIKU_MODEL`, so compaction and subagents do not
call a different model on the gateway. The `model_guest` check sends one
bounded `POST /v1/messages` (`max_tokens` 1) for that id. HTTP 429 is
`unavailable`: the catalog can be ready while the model is in quota
cooldown. `GET /v1/models` is not proof the guest can prompt. `GET /healthz`
only proves the process is listening.

Process-driver review (`-driver`) is test/dev. It is not this topology.
Public-repository unattended sessions (review, run, scheduled, implement
on `visibility: public`) use this container topology. They do not fall
back to the process driver or the experimental local profile when Docker
or Podman is missing ([ADR 0022](decisions/0022-public-repo-isolation.md)).
A second isolation runtime is not part of the supported topology
([ADR 0028](decisions/0028-container-isolation-profile.md)).

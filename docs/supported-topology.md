# Supported topology

> **Explanation.** This page describes the supported unattended profile and
> the limits of its evidence. Follow the [operator guide](operator.md) to
> configure and run it.

The unattended proof is one tuple (D1/D4). Other OS/runtime/image
combinations are not claimed:

| Piece | Supported value |
| --- | --- |
| Host OS | Linux or macOS |
| Runner | one `rusui-runner` on the same host as the plane ([ADR 0029](decisions/0029-single-host-runner.md)) |
| Runtime | Docker or Podman CLI (`docker`/`podman` on `PATH`) |
| Guest | `$RUSUI_GUEST_IMAGE` (Grok ACP, Claude Code CLI 2.1.283, or Codex app-server). `RUSUI_GUEST=shikigami` spawns `shikigami --state ./state acp`; the reference image does not bake that binary. |
| Egress | `trusted`: HTTPS to `rusui.plane` only |
| Listen | loopback `127.0.0.1` |

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

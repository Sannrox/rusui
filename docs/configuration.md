# Configuration reference

Authoritative behavior is `cmd/rusui`, `cmd/rusui-runner`, `internal/policy`,
and `internal/server`. Intent: [ARCHITECTURE.md](../ARCHITECTURE.md).

## `rusui` flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `-addr` | `127.0.0.1:8080` | Listen address. Not restricted to loopback. |
| `-db` | `rusui.db` | SQLite path. |
| `-policy` | `policy.yaml` | Policy file. Reloaded by Slack `reload` or process restart. |
| `-allow-insecure` | false | Start without webhook, worker, and Slack secrets (warns). |
| `-version` | false | Print version and exit. |

Subcommand `rusui diagnose [-policy PATH] [-addr ADDR] [-url URL]` prints a
JSON topology report (`ready` / `misconfigured` / `unavailable`) and exits
0 only when every blocking check is ready. It does not print secret values.

`rusui drain -url URL -token TOKEN` POSTs `/drain` (worker auth), pauses new
claims, and lists leased turns. Do not open `rusui.db` while the plane holds it.
`rusui diagnostics` writes a redacted bundle (`diagnostics.json`).
Upgrade procedure: [upgrade.md](upgrade.md).

## `rusui-runner` flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `-url` | `http://127.0.0.1:8080` | Plane base URL. |
| `-repo` | (required) | `owner/name` to claim. |
| `-token` | `$RUSUI_WORKER_SECRET` | Bootstrap bearer token. |
| `-name` | `local` | Runner name. |
| `-driver` | (required unless `-acp`) | Process driver command (space-separated). |
| `-acp` | false | Host one ACP turn with Grok instead of the process driver. |
| `-once` | false | Claim at most one turn and exit. |
| `-version` | false | Print version and exit. |

Need `-repo` and `-driver`, or `-repo` and `-acp`.

## Environment variables

| Variable | Required | Notes |
| --- | --- | --- |
| `RUSUI_GITHUB_TOKEN` or `GITHUB_TOKEN` | **Yes** | Server exits if both empty |
| `RUSUI_WEBHOOK_SECRET` | **Yes** unless `-allow-insecure` | GitHub + generic event HMAC |
| `RUSUI_WORKER_SECRET` | **Yes** unless `-allow-insecure` | Runner / job bearer |
| `RUSUI_OPERATOR_TOKEN` | console HTML (M4) | Distinct from worker; unset keeps console HTML disabled ([ADR 0012](decisions/0012-operator-access.md)) |
| `RUSUI_SLACK_SECRET` | **Yes** unless `-allow-insecure` | Slack HMAC |
| `RUSUI_GITHUB_API` | no | Default `https://api.github.com` |
| `RUSUI_GITHUB_HOOK_IDS` | for reconcile | `owner/repo=hookid`, comma-separated |
| `RUSUI_SLACK_USERS` | for inbound Slack | Empty allowlist → 403 |
| `RUSUI_SLACK_BOT_TOKEN` | optional outbound | Exception `chat.postMessage` |
| `RUSUI_SLACK_CHANNEL` | optional outbound | Exception channel |
| `RUSUI_TLS_CERT` / `RUSUI_TLS_KEY` | container guests | Plane TLS identity. Unset disables the container driver. |
| `RUSUI_PLANE_CA` | container guests | CA file mounted at `/usr/local/share/ca-certificates/rusui-plane.crt`. |
| `RUSUI_GUEST_IMAGE` | container guests | Guest image identity. Empty fails closed. |
| `XAI_API_KEY` or `RUSUI_XAI_API_KEY` | model proxy | Plane secret; never copied into the guest. |

GitHub token: read-only. Do not give it to the runner or the model.
Guest `XAI_API_KEY` and git HTTP auth are the per-turn grant.

## HTTP

| Method | Path | Auth |
| --- | --- | --- |
| `GET` | `/healthz` | none; body `ok` |
| `GET` | `/readyz` | none; JSON topology report; 200 ready / 503 not ready |
| `POST` | `/drain` | worker secret; pause claims and list live turns |
| `POST` | `/hooks/github` | `X-Hub-Signature-256` |
| `POST` | `/hooks/events` | `X-Rusui-Signature-256` (same webhook secret); 202 |
| `POST` | `/hooks/slack` | Slack HMAC + user allowlist |
| `POST` | `/runners/hello` | worker secret |
| `POST` | `/sessions/{id}/events` | turn/session auth |
| `POST` | `/turns/{id}/actions` | turn auth |
| `POST` | `/jobs/claim` | `Authorization: Bearer` or `X-Worker-Token` |
| `POST` | `/jobs/{id}/heartbeat` | same |
| `POST` | `/jobs/{id}/complete` | same |
| `POST` | `/jobs/{id}/fail` | same |
| `POST` | `/sessions/{id}/turns` | worker secret |
| `POST` | `/sessions/{id}/cancel` | worker secret |
| `GET` | `/approvals/{id}` | worker secret or turn token |
| `*` | `/model-proxy/` | per-turn grant (HTTPS for container guests) |
| `*` | `/git-proxy/github.com/` | prepare or turn grant; push only with a turn grant on the session ref prefix |

GitHub webhook events: `issues`, `pull_request`, `issue_comment`.
Forward the tunnel to `http://127.0.0.1:8080/hooks/github`.

## Policy schema

`policy.yaml` is the only executable baseline. Unknown fields fail closed.
Policy v2 is keyed by **project** ([ADR 0005](decisions/0005-policy-v2-project.md)).

Operator default: [policy.example.yaml](../policy.example.yaml). Tests that
need eligible dry-run comments/close load [policy.fixture.yaml](../policy.fixture.yaml).

```yaml
version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
  review: true
  comments: false
  close: false
  implement: false
  land: false
  max_reviews_per_repo_per_utc_day: 50
projects:
  rusui:
    repos:
      owner/name:
        visibility: public
        review: true
        comments: false
        close: false
        implement: false
        land: false
```

`visibility` is a policy attribute of the **bound GitHub repository**, not of
this source repo. Use `private` when that bound repo is private.

Project `budgets` may set `max_concurrent_leases` (integer ≥ 1, default 1).
Any other budget key fails closed at parse.

Overlay pause may only **narrow**. Slack `reload` or process restart re-reads
the file.

## Slack commands

Slash command `/rusui` → `POST /hooks/slack`.

| Text | Effect |
| --- | --- |
| `status` | Queue / lease summary |
| `pause [project]` | Stop new claims and apply inserts (project slug) |
| `resume [project]` | Clear overlay pause |
| `sweep [repo]` | Enqueue refresh requests (`owner/name`) |
| `retry repo` or `retry repo#item` | Requeue `state=failed` |
| `reload` | Re-read the policy file |
| `implement` | Rejected |

If the Slack bot token is unset, exceptions stay in the process log.

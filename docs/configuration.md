# Configuration reference

Authoritative behavior is `cmd/rusui`, `cmd/rusui-runner`, `internal/policy`,
and `internal/server`. Intent: [ARCHITECTURE.md](../ARCHITECTURE.md).

## `rusui` flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `-addr` | `127.0.0.1:8080` | Listen address. Not restricted to loopback. |
| `-db` | `rusui.db` | SQLite path. |
| `-policy` | `policy.yaml` | Policy file. Reloaded by Slack `reload` or process restart. |
| `-env-idle-sleep` | `5m` | Sleep managed container environments after this idle period; `0` disables automatic sleep. |
| `-allow-insecure` | false | Start without webhook, worker, and Slack secrets (warns). |
| `-version` | false | Print version and exit. |

Subcommand `rusui diagnose [-policy PATH] [-addr ADDR] [-url URL]` prints a
JSON topology report (`ready` / `misconfigured` / `unavailable`) and exits
0 only when every blocking check is ready. It does not print secret values.

Policy tools run locally and do not start the server:

```bash
rusui policy init [-file policy.yaml]
rusui policy explain -repo OWNER/REPO [-policy policy.yaml] [-db rusui.db]
rusui policy simulate review -repo OWNER/REPO -item NUMBER [-policy policy.yaml] [-db rusui.db]
```

`policy init` creates the conservative operator starter file and refuses to
overwrite an existing file. `policy explain` prints effective repository and
project values with their source, plus global and project pauses and the
current UTC-day review count. `policy simulate review` applies the same pure
policy and pause and budget checks used by the server to a prospective review.
It reads the SQLite database in read-only mode. It does not contact GitHub,
admit a job, or start a guest. It does not inspect whether the item is already
queued or whether another turn holds the project lease cap. If the database
path does not exist, the explanation reports an empty pause and count state;
pass the server's actual `-db` path to include durable state.

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
| `-acp` | false | Host one ACP turn with the plane's guest (Grok or Claude Code) instead of the process driver. |
| `-ca` | `$RUSUI_PLANE_CA` | Plane CA; required for an `https` plane URL (setup-generated TLS). |
| `-once` | false | Claim at most one turn and exit. |
| `-version` | false | Print version and exit. |

Need `-repo` and `-driver`, or `-repo` and `-acp`.

## Environment variables

| Variable | Required | Notes |
| --- | --- | --- |
| `RUSUI_GITHUB_TOKEN` or `GITHUB_TOKEN` | **Yes** | Server exits if both empty |
| `RUSUI_WEBHOOK_SECRET` | **Yes** unless `-allow-insecure` | GitHub + generic event HMAC |
| `RUSUI_WORKER_SECRET` | **Yes** unless `-allow-insecure` | Runner / job bearer |
| `RUSUI_OPERATOR_TOKEN` | operator console and `rusui attach` | Distinct from worker; unset keeps console HTML and attachment disabled ([ADR 0012](decisions/0012-operator-access.md)) |
| `RUSUI_SLACK_SECRET` | **Yes** unless `-allow-insecure` | Slack HMAC |
| `RUSUI_GITHUB_API` | no | Default `https://api.github.com` |
| `RUSUI_GITHUB_HOOK_IDS` | for reconcile | `owner/repo=hookid`, comma-separated |
| `RUSUI_SLACK_USERS` | for inbound Slack | Empty allowlist → 403 |
| `RUSUI_SLACK_BOT_TOKEN` | optional outbound | Exception `chat.postMessage` |
| `RUSUI_SLACK_CHANNEL` | optional outbound | Exception channel |
| `RUSUI_TLS_CERT` / `RUSUI_TLS_KEY` | container guests | Plane TLS identity. Unset disables the container driver. |
| `RUSUI_PLANE_CA` | container guests | CA file mounted at `/usr/local/share/ca-certificates/rusui-plane.crt`. |
| `RUSUI_GUEST_IMAGE` | container guests | Guest image identity. Empty fails closed. |
| `XAI_API_KEY` or `RUSUI_XAI_API_KEY` | model proxy (Grok) | Plane secret; never copied into the guest. |
| `RUSUI_ANTHROPIC_API_KEY` or `ANTHROPIC_API_KEY` | model proxy (Claude Code) | Plane secret, sent upstream as `x-api-key`; never copied into the guest. |
| `RUSUI_GUEST` | no | `grok` (default) or `claude`; picks the model provider ([ADR 0017](decisions/0017-claude-guest-and-model-upstream.md)). |
| `RUSUI_MODEL_UPSTREAM` | no | http(s) URL replacing the provider API, e.g. a gateway or a CLI proxy on the plane host. With it set, the provider key may be empty. |
| `RUSUI_AGENT_GITHUB_TOKEN` | `implement` sessions | Your GitHub credential for agent publication ([ADR 0015](decisions/0015-agent-publication.md)). Given only to implement sessions — `run` sessions started for an open pinned task (`rusui run -effort …`) on repositories with `implement: true` — as `GH_TOKEN` and git's credential for `https://github.com`. Unset: no session can push. |
| `RUSUI_DISABLE_COAUTHOR_TRAILER` | no | `1` drops `Co-authored-by: rusui <noreply@rusui.invalid>` from agent commits. |
| `RUSUI_DISABLE_SESSION_TRAILER` | no | `1` drops `Rusui-Session: <session id>` from agent commits. |
| `SUMIKA_SOCK` | local interactive profile | Optional same-user Unix socket path; otherwise Sumika's per-user default path is used. |

### Model upstream

The guest only ever holds its per-turn grant; the plane model proxy strips
it and sends the operator's credential upstream. The upstream is, in order:

1. `RUSUI_MODEL_UPSTREAM` when set: a gateway or a **CLI proxy** that uses
   your own CLI login on the plane host, such as
   [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI). The provider
   key, if set, is forwarded as the proxy's client key.
2. The provider API (`https://api.x.ai` or `https://api.anthropic.com`)
   with the provider key.
3. Neither: the proxy answers 503.

Upstream failures answer 502 and are logged without the provider key, the
guest grant, or `RUSUI_MODEL_UPSTREAM` itself, which may carry a gateway
credential.

```bash
RUSUI_GUEST=claude
RUSUI_MODEL_UPSTREAM=http://127.0.0.1:8317     # CLI proxy on the plane host
RUSUI_ANTHROPIC_API_KEY=<the proxy's client key>
```

Run a CLI proxy on loopback with its own client key; rusui does not ship
or manage it. Whether a subscription may be used this way for automated
work is set by the provider's terms, not by rusui.

With plane TLS, every client of an `https` plane URL (`rusui-runner`, `rusui run`, `rusui review`, `sessions`, `logs`, `approvals`, `approve`, `prompt`, `drain`) trusts `RUSUI_PLANE_CA`.

GitHub token: read-only. Do not give it to the runner or the model.
Guest `XAI_API_KEY` and git HTTP auth are the per-turn grant, except in
`implement` sessions, where git and `gh` use `RUSUI_AGENT_GITHUB_TOKEN`.
Issue that as a fine-grained token limited to the bound repositories
(contents and pull requests write; no administration, workflow, or secrets)
and protect default branches. The guest image must provide `gh`.

Agent commits in `run` and `scheduled` sessions carry both trailers
([ADR 0015](decisions/0015-agent-publication.md) D3). The runner installs a
`commit-msg` hook through `core.hooksPath` that first runs the repository's
own hooks, then adds any trailer not already present. Trailers are
attribution only; a commit made with `--no-verify` or outside the session
will not carry them.

### Run turn results

The runner gives each run-turn guest `RUSUI_RESULT`, a file path. The
agent writes one JSON object there: `{"pull_request": N}` or
`{"blocked_reason": "..."}`. Prose is not a result; a turn without one
fails closed. The plane records its own outcome: `published` only when
GitHub shows pull request N at the workspace `HEAD`, otherwise
`unconfirmed` for a claimed pull request or `blocked` for a reported
reason. See [ARCHITECTURE.md](../ARCHITECTURE.md#process-boundary).

## HTTP

| Method | Path | Auth |
| --- | --- | --- |
| `GET` | `/healthz` | none; body `ok` |
| `GET` | `/readyz` | none; JSON topology report; 200 ready / 503 not ready |
| `GET` | `/sessions/{id}` | operator or worker token; session detail without environment receipt history |
| `GET` | `/sessions/{id}?include=receipts[&receipt_after_id={id}]` | operator or worker token; up to 100 receipts in ID order; follow `environment_receipts_next_after_id` when present |
| `GET` | `/sessions/{id}?view=review-status&turn_id={turn}` | operator or worker token; compact turn state and revisions for polling |
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

The current operator starter is the managed policy v2 profile: review, run,
and scheduled session kinds, with review enabled and comments, close,
implement, and land disabled. `comments` and `close` authorize dry-run
intended-actions only; rusui does not write either action to GitHub. The
`implement` field is an opt-in for the accepted agent-publication path and
requires the configured agent GitHub credential; it is off in the starter.
`land` remains unauthorized by the current contract and should stay false.
The test fixture's comments/close settings are for dry-run coverage, not an
operator profile. The `local` session kind is experimental, default-off, and
outside the supported 1.0 core; enabling it requires the explicit
`local_runtime` configuration below.

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

The experimental `local` session kind is absent from defaults. Enable it per
Project and configure the exact command and working directory in
`local_runtime`; both fields are required, `cwd` must be an absolute clean
path, and session-create requests cannot override them.

```yaml
projects:
  local:
    session_kinds: [local]
    local_runtime:
      argv: ["claude", "--continue"]
      cwd: "/path/to/project"
    repos: {}
```

The Rusui server and Sumika daemon must run as the same OS user on macOS or
Linux. Rusui connects to Sumika's local Unix socket and never injects managed
credentials into the local process. See [the local operator profile](operator.md#local-interactive-profile).

Project `budgets` may set `max_concurrent_leases` (integer ≥ 1, default 1).
Any other budget key fails closed at parse.

Project `permissions` answer the guest's `session/request_permission`
(the tool fence). A rule matches on `tool`, `kind`, and a `command`
substring; empty fields are wildcards. `action` is `allow` (the default) or
`reject`. Any matching reject rule wins over allow rules; a reject rule
must name at least one field. Unmatched requests wait for an operator
approval.

```yaml
projects:
  rusui:
    permissions:
      - command: npm
      - command: npm publish
        action: reject
```

Implement sessions also apply a built-in reject set, whatever policy
allows ([ADR 0017](decisions/0017-claude-guest-and-model-upstream.md) D3):
`gh pr merge|close|review`, `gh pr ready --undo`,
`gh release|repo|workflow|secret|variable|ruleset`, `gh api` with `-X`,
`--method`, `-f`, `--field`, `--raw-field`, or `--input`, and `git push`
naming `main`/`master`, forcing (`-f`, `--force*`, `+ref`), deleting
(`--delete`, `-d`, `:ref`), `--mirror`, `--all`, or `--prune`. Creating
`gh` aliases or extensions and git aliases (`git config alias.*`,
`git -c alias.*`) is rejected too. The command line is lexed like a shell:
quotes and escapes are removed, `;`, `&&`, `|`, subshells, and command
substitutions (also inside double quotes) are checked separately, and
`sh|bash|zsh|dash|ksh -c` and `eval` payloads are checked recursively.
Approvals in implement sessions are re-checked against the fence.

These rules see only the permission requests the guest makes. A bare
`git push` while the current branch is `main`, a command inside a script
file, a subcommand built from shell variables (`X=merge; gh pr $X 1`),
and an alias defined before the session are not caught; they are
workflow control, not a security boundary.

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

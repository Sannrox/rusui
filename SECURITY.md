# Security policy

Report vulnerabilities privately. Do not open a public issue, pull request, or
discussion for an exploitable defect.

Use a [GitHub security advisory](https://github.com/Sannrox/rusui/security/advisories/new).
Include the affected version or commit, the path (webhook, runner auth, Slack
signing, policy, apply), and a minimal reproduction. Do not attach live tokens,
webhook payloads with private data, or a copy of `rusui.db`.

## What this process is trusted to do

rusui is a **self-hosted operator tool**. The product contract is
[ARCHITECTURE.md](ARCHITECTURE.md). In short:

- GitHub is intake. The server is the source of truth. Slack is the human socket.
- Apply is **dry-run**: the GitHub client is read-only. The process never comments,
  closes, or merges.
- Model CLIs never receive GitHub write tokens.
- The HTTP server **defaults** to loopback (`127.0.0.1:8080`). That default is
  not a network jail. `-addr 0.0.0.0:8080` listens on every interface with no
  TLS.

## Secrets

The server **refuses to start** unless these are set, unless you pass
`-allow-insecure` (logs a warning; local experiments only):

| Variable | Purpose |
| --- | --- |
| `RUSUI_WEBHOOK_SECRET` | HMAC for `POST /hooks/github` and `POST /hooks/events` |
| `RUSUI_WORKER_SECRET` | Bearer token for runner / job endpoints |
| `RUSUI_SLACK_SECRET` | Slack signing secret |

Also required to start: `RUSUI_GITHUB_TOKEN` or `GITHUB_TOKEN` (read-only).
`RUSUI_SLACK_USERS` is the inbound allowlist (Slack user ids). An empty
allowlist 403s every slash command.

Never commit tokens, webhook secrets, worker secrets, Slack secrets, local
SQLite databases, `.version`, or `_output/`.

Do not pass `-addr 0.0.0.0:8080` or `-allow-insecure` on a reachable network.

## Scope

In scope: webhook signature bypass, runner authentication bypass, Slack replay
or allowlist bypass, secret leakage into a guest environment, unintended
GitHub mutation, private context leaking into a public-repo job.

Out of scope: the operator tunnel, GitHub or Slack themselves, and guest CLIs
(Grok, Codex, and so on).

## Supported versions

There is no numbered release yet. Report against `main`.

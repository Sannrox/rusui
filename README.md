# Rusui (留守居)

Personal GitHub maintenance control plane. You write `policy.yaml`. The
server admits work, records immutable reviews, and dry-runs apply.

留守居 is the steward who keeps house while you are away.

See `ARCHITECTURE.md` and `BUILD.md`.

## Run (loopback)

```bash
go test ./...
go run ./cmd/rusui -addr 127.0.0.1:8080 -policy policy.yaml -db rusui.db
go run ./cmd/rusui-worker -url http://127.0.0.1:8080 -repo Sannrox/rusui
```

GitHub and Slack cannot reach loopback. Point a tunnel at the process.
Webhook path ACKs without fetching GitHub; refresh runs asynchronously.

Copy `policy.example.yaml` to `policy.yaml`. Tests use the fixture policy
(comments/close simulation on).

## GitHub credentials

Read-only. Do not give the model process this token.

| Variable | Purpose |
|---|---|
| `RUSUI_GITHUB_TOKEN` or `GITHUB_TOKEN` | Bearer token for the REST API |
| `RUSUI_GITHUB_HOOK_IDS` | `owner/repo=hookid` pairs, comma-separated; required for delivery reconcile |
| `RUSUI_GITHUB_API` | API base URL (default `https://api.github.com`) |
| `RUSUI_WEBHOOK_SECRET` | `X-Hub-Signature-256` for `POST /hooks/github` |
| `RUSUI_WORKER_SECRET` | Bearer token for `/jobs/*` |
| `RUSUI_SLACK_SECRET` | Slack signing secret (`X-Slack-Signature`) |
| `RUSUI_SLACK_USERS` | Comma-separated Slack `user_id` allowlist |
| `RUSUI_SLACK_BOT_TOKEN` | Bot token for exception `chat.postMessage` (optional) |
| `RUSUI_SLACK_CHANNEL` | Channel for exceptions |

Slash command `/rusui` → `POST /hooks/slack`:

```
status [repo]
pause [repo]
resume [repo]
sweep [repo]
retry repo[#item]
reload
```

`implement` is rejected. GitHub webhooks do not require Slack. If Slack
secrets are unset, exceptions stay in the process log.

The client is read-only: issues, pulls, refs, compare, and hook deliveries.
It never comments, closes, or merges. Default-branch reachability uses
`GET /repos/.../compare/{commit}...{defaultTip}` (`ahead` or `identical`).

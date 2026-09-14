# Rusui (留守居)

Personal GitHub maintenance control plane. You write `policy.yaml`. The
server admits work, records immutable reviews, and dry-runs apply.

留守居 is the steward who keeps house while you are away.

See `ARCHITECTURE.md` and `BUILD.md`. The proposed direction beyond v1 is
in `VISION.md`, `ROADMAP.md`, and `docs/decisions/`.

## Build

Run this before committing:

```bash
make all && make test && make validate
```

| Target | What it does |
|---|---|
| `make all` | Host-platform binaries into `_output/local/bin/$(go env GOOS)/$(go env GOARCH)/` |
| `make test` | Unit tests (`COVER=1` writes coverage HTML) |
| `make validate` | golangci-lint, govulncheck, go-fix, shellcheck |
| `make update` | Apply go-fix modernizations |
| `make release-images` | Docker build image, linux binaries, `rusui`/`rusui-worker` runtime images |
| `make docker-clean` | Remove docker build containers/tags and `_output` |
| `make clean` | Remove `_output` |

Dockerized Makefile (reproducible toolchain):

```bash
./build/run.sh make all
./build/run.sh make test
./build/run.sh make validate
make release-images
```

See `build/README.md`. Runtime images still bind loopback unless you pass `-addr 0.0.0.0:8080`.

```bash
make all WHAT=cmd/rusui
make test WHAT=./internal/engine GOFLAGS="-v" TEST_ARGS='-run ^TestClaim$$'
make test COVER=1
```

## Run (loopback)

```bash
make all
_output/local/bin/$(go env GOOS)/$(go env GOARCH)/rusui -addr 127.0.0.1:8080 -policy policy.yaml -db rusui.db
_output/local/bin/$(go env GOOS)/$(go env GOARCH)/rusui-worker -url http://127.0.0.1:8080 -repo Sannrox/rusui
```

GitHub and Slack cannot reach loopback. Point a tunnel at the process.
Webhook path ACKs without fetching GitHub; refresh runs asynchronously.
At startup the server expires hung refresh owners, reconciles missed
hook deliveries, catches up open and locally tracked items, and retries
unpublished apply attempts. The same paths repeat: refresh every 1s,
reconcile every 5m, catch-up every 15m, apply retry every 1m. Reconcile
is skipped with a log when `RUSUI_GITHUB_HOOK_IDS` is unset.

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

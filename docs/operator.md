# Operator guide

Run rusui as a single operator on your own machine. Apply is dry-run: nothing
is commented, closed, or merged on GitHub.

Flags, env, HTTP, and policy: [configuration.md](configuration.md).
Nouns: [CONTEXT.md](../CONTEXT.md).

## Prerequisites

- Go 1.26 on `PATH`. `make` uses `GOTOOLCHAIN=go$(cat .go-version)`
  (currently 1.26.6).
- A **read-only** GitHub token for repositories listed in policy.
- A tunnel or webhook relay if GitHub or Slack must reach the process.
- For review turns: a process driver command (`-driver`) or `-acp` (Grok ACP).

## 1. Build and policy

```bash
git clone https://github.com/Sannrox/rusui.git
cd rusui
cp policy.example.yaml policy.yaml
# edit projects / bound repos
make all
```

Policy is version 2, keyed by **project**. `comments: true` / `close: true`
authorize **simulated** intended-action rows, not GitHub writes.

## 2. Secrets

Required or the server exits (unless `-allow-insecure`):

```bash
export RUSUI_GITHUB_TOKEN=ghp_...
export RUSUI_WEBHOOK_SECRET="$(openssl rand -hex 16)"
export RUSUI_WORKER_SECRET="$(openssl rand -hex 16)"
export RUSUI_SLACK_SECRET="$(openssl rand -hex 16)"   # placeholder if Slack is unused
```

Set the webhook secret to the same value GitHub stores on the webhook. Set
the worker secret on **both** the server and `rusui-runner`. A random
`RUSUI_SLACK_SECRET` is only enough to *start* the process; inbound Slack
HMAC will fail until you replace it with the Slack app signing secret and
restart. See [SECURITY.md](../SECURITY.md).

## 3. Run (loopback)

```bash
BIN="_output/local/bin/$(go env GOOS)/$(go env GOARCH)"
"$BIN/rusui" -addr 127.0.0.1:8080 -policy policy.yaml -db rusui.db
```

In another terminal:

```bash
BIN="_output/local/bin/$(go env GOOS)/$(go env GOARCH)"
"$BIN/rusui-runner" \
  -url http://127.0.0.1:8080 \
  -repo OWNER/REPO \
  -token "$RUSUI_WORKER_SECRET" \
  -driver ./review-driver
```

`GET http://127.0.0.1:8080/healthz` must return `ok`.

Do not pass `-addr 0.0.0.0:8080` unless secrets are set. There is no TLS.

At startup the server expires hung refresh owners, reconciles missed hook
deliveries, catches up open and locally tracked items, and retries unpublished
apply attempts. The same paths repeat: refresh every 1s, reconcile every 5m,
catch-up every 15m, apply retry every 1m. Reconcile is skipped with a log when
`RUSUI_GITHUB_HOOK_IDS` is unset.

SQLite defaults to `rusui.db` in the current working directory (`*.db` is
gitignored).

## 4. GitHub webhook

Create a **repository** webhook:

- Payload URL: `https://<your-tunnel>/hooks/github`
- Content type: `application/json`
- Secret: `$RUSUI_WEBHOOK_SECRET`
- Events: `issues`, `pull_request`, `issue_comment`

The handler ACKs 2xx after committing `delivery_id` and a refresh request. It
does not fetch GitHub on that path.

Generic signed intake: `POST /hooks/events` with `X-Rusui-Signature-256`
(same secret), store, then 202.

`RUSUI_GITHUB_HOOK_IDS=owner/repo=123` is required for delivery reconcile.

## 5. Tunnel

GitHub and Slack cannot reach `127.0.0.1`. Point a tunnel at the process.

cloudflared (example):

```bash
cloudflared tunnel --url http://127.0.0.1:8080
# webhook:  https://<id>.trycloudflare.com/hooks/github
# Slack:    https://<id>.trycloudflare.com/hooks/slack
```

smee (example):

```bash
smee -u https://smee.io/<id> -t http://127.0.0.1:8080/hooks/github
```

## 6. Slack (optional for GitHub intake)

The Slack **signing secret** is still required at process start. GitHub
webhooks do not call Slack. If you want slash commands:

1. Slash command `/rusui` posting to `https://<tunnel>/hooks/slack`.
2. Set `RUSUI_SLACK_SECRET` to the Slack app **signing secret** (not the
   random placeholder from step 2) and restart `rusui`.
3. `export RUSUI_SLACK_USERS=U0123ABCD,U0456EFGH` — Slack **user ids**.
   An empty allowlist 403s everyone.
4. Optional exceptions: `RUSUI_SLACK_BOT_TOKEN` and `RUSUI_SLACK_CHANNEL`.

Commands: `status [repo]`, `pause [repo]`, `resume [repo]`, `sweep [repo]`,
`retry repo` or `retry repo#item`, `reload`. `implement` is rejected.

`retry` requeues `state=failed` on unchanged content. `sweep` only enqueues
refreshes.

## 7. Images

Runtime images still bind loopback **inside the container**. Publish a host
port only after `-addr 0.0.0.0:8080` and with secrets set.
See [development.md](development.md).

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Server exits immediately | Missing GitHub token or webhook/worker/Slack secret; or use `-allow-insecure` locally |
| `GET /healthz` fails | Process not listening; wrong `-addr` |
| GitHub webhook 401 | Secret mismatch, or tunnel not hitting `/hooks/github` |
| Runner `auth` / 401 | Secret mismatch between processes |
| Slack 403 `user` | `RUSUI_SLACK_USERS` missing that user id, or empty |
| Slack 401 | Signing secret / timestamp skew > 5 minutes |
| Claim never returns work | Repo not bound in policy, paused, or daily budget exhausted |
| Reconcile skipped | `RUSUI_GITHUB_HOOK_IDS` unset |
| No GitHub comment appeared | Apply is dry-run; look at intended-actions in SQLite |
| Policy change ignored | Slack `reload` or restart |

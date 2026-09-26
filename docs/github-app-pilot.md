# Review-only GitHub App behind a local tunnel

Connect one public test repository through a GitHub App and a tunnel to
loopback rusui. Policy stays review-only. Apply is dry-run: rusui does
not comment, close, implement, or land on GitHub.

First loopback session: [tutorial.md](tutorial.md). Supported install
topology: [operator.md](operator.md). Drain and diagnose:
[upgrade.md](upgrade.md).

Do not put tokens, PEM keys, webhook secrets, or private repository
names in this file, in policy, or in screenshots.

## App permissions, events, and secrets

Create a GitHub App on an account you control. Install it on **one**
public test repository. Grant only read permissions:

| Permission | Access | Why |
| --- | --- | --- |
| Metadata | Read | Repository identity (automatic) |
| Contents | Read | Default-branch SHA and git refs |
| Issues | Read | Issue intake |
| Pull requests | Read | Pull-request intake |
| Webhooks | Read | Optional. Only for repository-webhook delivery reconcile with `RUSUI_GITHUB_HOOK_IDS` |

Do not grant contents, issues, pull-request, or administration write.
Do not set `RUSUI_AGENT_GITHUB_TOKEN` for this pilot.

Subscribe the App to `issues`, `pull_request`, and `issue_comment`.
Set the App webhook:

- URL: `https://<tunnel-host>/hooks/github`
- Content type: `application/json`
- Secret: the same value as `RUSUI_WEBHOOK_SECRET`

Plane secrets (no values in examples):

| Variable | Role |
| --- | --- |
| `RUSUI_GITHUB_APP_ID` | App id |
| `RUSUI_GITHUB_APP_PRIVATE_KEY` or `RUSUI_GITHUB_APP_PRIVATE_KEY_FILE` | App PEM |
| `RUSUI_GITHUB_APP_INSTALLATION_ID` | Installation on the test repository |
| `RUSUI_WEBHOOK_SECRET` | HMAC for `POST /hooks/github` |
| `RUSUI_WORKER_SECRET` | Runner bearer |
| `RUSUI_SLACK_SECRET` | Required at process start even if Slack is unused |

When the App env is set, rusui mints installation tokens and does not
need `RUSUI_GITHUB_TOKEN`. Keep the listen address on loopback.

## Review-only policy

Copy [policy.example.yaml](../policy.example.yaml). Bind the test
repository under a project. Keep:

```yaml
review: true
comments: false
close: false
implement: false
land: false
```

`comments` and `close` authorize dry-run intended-actions only. They do
not write to GitHub even if you later set them true.

## Tunnel

GitHub cannot reach `127.0.0.1`. One supported relay is a temporary
cloudflared quick tunnel forwarding to the loopback process:

```bash
cloudflared tunnel --url http://127.0.0.1:8080
```

Use the printed `https://*.trycloudflare.com` host as the App webhook
base. Verify:

```bash
curl -fsS http://127.0.0.1:8080/healthz
# expect: ok
```

GitHub's webhook recent-deliveries list should show 2xx for
`POST /hooks/github` after you open or comment on a test issue.

Cleanup: stop `cloudflared` (SIGINT). Remove the App webhook URL or
uninstall the App from the test repository. The quick tunnel hostname
dies with the process; do not leave a live URL in the App settings.

smee is an alternative relay aimed at `/hooks/github` only; see
[operator.md](operator.md#5-tunnel).

## One review and a receipt

1. `rusui diagnose` exit 0 on the supported topology
   ([operator.md](operator.md#supported-topology)).
2. Start rusui on `127.0.0.1:8080` and `rusui-runner -acp` for the bound
   repo.
3. Open a pull request or issue on the test repository, or start one
   pull-request review with `rusui review OWNER/REPO#N` (that command
   POSTs `/reviews` and waits for the stored artifact).
4. For a webhook-started review, list sessions and read `review_result`
   on the session JSON.

```bash
BIN="_output/local/bin/$(go env GOOS)/$(go env GOARCH)"
"$BIN/rusui" sessions -url http://127.0.0.1:8080 -token "$RUSUI_WORKER_SECRET"
curl -fsS -H "Authorization: Bearer $RUSUI_WORKER_SECRET" \
  http://127.0.0.1:8080/sessions/SESSION_ID
# inspect review_result
```

5. On GitHub, confirm no new comment, close, or pull request from rusui.
   Intended-actions in SQLite are dry-run records, not GitHub writes.
   The intake client only GETs.

## Failure checks

Invalid webhook signature. A POST with the wrong HMAC is 401 `bad sig`.
The plane does not ACK or enqueue refresh:

```bash
curl -sS -o /dev/null -w '%{http_code}\n' \
  -X POST http://127.0.0.1:8080/hooks/github \
  -H 'Content-Type: application/json' \
  -H 'X-Hub-Signature-256: sha256=deadbeef' \
  -d '{"repository":{"full_name":"OWNER/REPO"},"issue":{"number":1}}'
# expect: 401
```

Missing App permissions. A 403/404 from GitHub on issue, pull, or
contents GET fails that refresh. Widen nothing; add only the read
permission named in the table above and retry. Do not fall back to a
personal token with write scope.

Unavailable tunnel. GitHub shows failed App deliveries. rusui does not
see the POST. `GET /healthz` stays `ok` on loopback. Open-item catch-up
recovers issues and pulls that are still open once the App token path
works. An item opened and closed during the outage is not recovered
from the App webhook. Delivery reconcile (`GET /repos/.../hooks/{id}/deliveries`)
runs only when `RUSUI_GITHUB_HOOK_IDS` names a **repository** webhook;
it does not read GitHub App hook deliveries.

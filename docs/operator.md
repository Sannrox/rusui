# Operator guide

Run rusui against a real repository. This how-to covers setup, the main
loopback path, GitHub intake, optional Slack, restart, removal, and
troubleshooting. Apply is dry-run: nothing is commented, closed, or merged
on GitHub.

For a first credential-free session, start with [the tutorial](tutorial.md).
For flags, policy fields, and HTTP behavior, see [configuration](configuration.md).
For turn measurements and review-result dispositions, see the
[review results reference](review-results.md).
<a id="supported-topology"></a>
For the supported unattended profile and its boundaries, see the
[supported topology explanation](supported-topology.md).
For drain, packaged upgrade, and redacted diagnostics, see [upgrade](upgrade.md).

## Optional capability guides

Read these separate procedures when you need the capability:

### Local interactive profile

<a id="local-interactive-profile"></a>
Use the [local interactive how-to](local-interactive.md).

### Container guests (trusted egress)

Use the [container guest how-to](container-guests.md).

### On-demand pull request review

Use the [pull request review how-to](pull-request-review.md).

### Implement sessions (agent publication)

Use the [implement session how-to](implement-sessions.md).

## Prerequisites

- Go 1.26 on `PATH`. `make` uses `GOTOOLCHAIN=go$(cat .go-version)`
  (currently 1.26.6).
- A **read-only** GitHub token for intake of repositories listed in policy.
  `implement` sessions additionally need your GitHub credential in
  `RUSUI_AGENT_GITHUB_TOKEN`: a fine-grained token with contents and
  pull-request write on the bound repositories only (ADR 0015). The guest
  image must provide `gh`.
- A tunnel or webhook relay if GitHub or Slack must reach the process.
- For review turns: `-acp` (the plane's guest: Grok by default; for
  `RUSUI_GUEST=claude` the image or `PATH` needs the Claude Code CLI
  2.1.283; for `codex`, the `codex` CLI; for `shikigami`, `shikigami`
  on `PATH` speaking `shikigami --state ./state acp`) or a process
  driver command (`-driver`) that prints review JSON on stdout. There
  is no in-tree `review-driver` binary.

For shikigami HTTP prompt attachments, the compatibility check is the guest's
`initialize.agentCapabilities.promptCapabilities`: `image: true` for images,
`embeddedContext: true` for PDF and text documents. This requires the behavior
introduced in [shikigami #410](https://github.com/Sannrox/shikigami/pull/410).
The runner records that response with the session's turn actions. Until a text
turn has initialized the guest, attachments receive HTTP 400 without storing
a prompt. Missing or false capabilities also receive 400. Each subsequent
guest process checks its own response before sending attachments, including
after an image or binary downgrade. `RUSUI_GUEST_VERSION` remains measurement
metadata; it does not bypass this check. The prompt limits remain 8 MiB per
part and 16 MiB aggregate.

## Set up with `rusui setup`

`rusui setup` prepares this machine ([ADR 0018](decisions/0018-rusui-setup.md)).
Build rusui first (step 1), then:

```bash
rusui setup plan    # what would change; writes nothing
rusui setup apply   # idempotent; ends with rusui diagnose
```

`apply` creates the state directory (`0700`; `-state DIR` overrides the
platform default), plane TLS (a local CA and a certificate for
`127.0.0.1`, `localhost`, and `rusui.plane`), `rusui.env` (`0600`) with
generated worker, webhook, Slack, and operator secrets, a policy skeleton
when none exists, and, when Docker or Podman is installed, the
`rusui-trusted` network and the reference guest image. The image
(`build/guest-image`: git, `gh`, Node.js 22, Go 1.26.6, Chromium, Claude Code CLI 2.1.283) is
tagged by its image ID, so the tag names the bits the guest runs (the
Dockerfile hash is only the build cache key); `-rebuild-image` rebuilds it
with `--pull --no-cache`. Setup records the tag as `RUSUI_GUEST_IMAGE`
and sets `RUSUI_GUEST=claude` unless you chose another image or guest.
`make guest-image` builds the same tag from a checkout. It never overwrites a policy or existing TLS
(`-rotate-tls` replaces TLS only) and never prints secret values.

Lines marked `needs-you` are inputs only you can supply: the GitHub token
and model access (a key, or `RUSUI_MODEL_UPSTREAM` for a CLI proxy you
logged in to), plus a guest image when no container runtime is installed.
Put them in `rusui.env`, then rerun `rusui setup apply`; it starts or updates
the user service and checks readiness. Linux setup enables account-wide user
lingering so the service starts at boot and continues after logout. See
[user-service.md](user-service.md) for service behavior and removal. The
sections below describe the same setup steps by hand.

For a first pinned implement task, follow the
[implement session how-to](implement-sessions.md#first-pinned-implement-task).
The timed macOS walkthrough record is retained as
[historical evidence](proofs/operator-timed-macos-walkthrough.md).

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

Project `secrets:` names ids the guest may hold while awake. Put each
value in `RUSUI_SECRET_<ID>` on the plane (`RUSUI_SECRET_NPM_TOKEN` for
`npm_token`). The guest sees the value at `/run/rusui/secrets/<id>` and
as `$NPM_TOKEN` during the wake. Sleep removes it. The receipt names the
id, not the value.

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
  -acp
```

To serve more than one policy-bound repository, repeat `-repo`. The runner
rotates the first repository it checks on each poll, skips a repository that
is paused or at its lease budget for that poll, and keeps each turn in its own
workspace. Existing single-`-repo` commands continue to work:

```bash
"$BIN/rusui-runner" \
  -url http://127.0.0.1:8080 \
  -repo OWNER/REPO-A \
  -repo OWNER/REPO-B \
  -token "$RUSUI_WORKER_SECRET" \
  -acp
```

A project with no bound repository is claimed with `-project SLUG` instead
of `-repo` ([ADR 0048](decisions/0048-pinless-run-session.md)). `-project`
and `-repo` are exclusive.

Every listed repository must still be enabled by the plane policy. An
unconfigured repository fails closed; the runner never enumerates or claims
repositories outside its explicit `-repo` list.

`GET http://127.0.0.1:8080/healthz` must return `ok`. Then:

```bash
"$BIN/rusui" diagnose -policy policy.yaml -addr 127.0.0.1:8080
curl -fsS http://127.0.0.1:8080/readyz
"$BIN/rusui" run -project rusui -token "$RUSUI_WORKER_SECRET" "bounded session"
```

`run` creates a `run` session through the public API. Container turns still
need the guest image, plane TLS, and a runner.

List sessions and send prompts with the CLI:

```bash
"$BIN/rusui" sessions -url http://127.0.0.1:8080 -token "$RUSUI_WORKER_SECRET"
"$BIN/rusui" prompt -url http://127.0.0.1:8080 -token "$RUSUI_WORKER_SECRET" SESSION_ID "continue with this change"
"$BIN/rusui" prompt -steer -url http://127.0.0.1:8080 -token "$RUSUI_WORKER_SECRET" SESSION_ID "use the narrower fix"
"$BIN/rusui" prompt -queue -url http://127.0.0.1:8080 -token "$RUSUI_WORKER_SECRET" SESSION_ID "then update the changelog"
"$BIN/rusui" prompt -drop-queue -url http://127.0.0.1:8080 -token "$RUSUI_WORKER_SECRET" SESSION_ID
```

`-queue` stores the prompt and starts it as the next turn once the current
turn completes or fails; queued prompts run in the order sent. A plain
follow-up also waits for the live turn, but it becomes the pending revision at
once and survives cancel. A queued prompt does not: `-drop-queue` drops every
queued prompt that has not started and leaves the current turn running, and
cancelling the session drops them too. With no turn running, a queued prompt
starts at once (`"held": false` in the response).

The session console offers the same queued follow-up and **Steer running
turn** actions. A steer without a live turn becomes a queued follow-up. For a
live turn, Rusui asks the guest to cancel its current prompt before sending the
operator prompt on the same ACP session. A pending permission wait is
cancelled; steering never approves it. If the live turn ends before the steer
completes, Rusui preserves the prompt as a queued follow-up, including when
the turn is explicitly cancelled.

Guest cancellation status:

| Guest | `session/cancel` status | Evidence |
| --- | --- | --- |
| In-tree fake ACP agent | Verified for the steer integration path | `TestSteerInterruptsLiveRunnerAndKeepsGuestSession` |
| Grok | Not verified | The live Grok test does not exercise `session/cancel` |
| Claude Code CLI 2.1.283 | Not verified | The live Claude test does not exercise cancel |

The fake-agent result proves rusui's host behavior only. Validate the guest
before relying on it to stop at a safe point.

Live interruption requires the ACP runner. A process-driver turn has no guest
ACP session, so a steer targeted at it stays durable and runs as a follow-up
after the live lease ends.

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

Commands: `status`, `pause [project]`, `resume [project]`, `sweep [repo]`,
`retry repo` or `retry repo#item`, `reload`. `implement` is rejected.
`pause rusui` is the project slug, not `Sannrox/rusui`.

`retry` requeues `state=failed` on unchanged content; without Slack, `rusui retry OWNER/REPO[#ITEM]` with the operator token does the same. `sweep` only enqueues
refreshes.

## 7. Images

Runtime images still bind loopback **inside the container**. Publish a host
port only after `-addr 0.0.0.0:8080` and with secrets set.
See [development.md](development.md).

## Restart, uninstall, data retention

Restart is the same command line. On start the process runs `Recover`
(expire refresh owners, strip nothing from a live DB except hung owners).
SQLite is the session truth; container dirt is not in the file.

Stop the process (SIGINT). Data that remains until you delete it:

- `rusui.db` plus `-wal`/`-shm` if present
- snapshot cache (`snapshots/` next to the database by default)

Secrets live in the operator environment, not in those files. Do not
embed token values in examples. Restore a copied database with
`rusui restore -db <copy>` (inventory, clear live leases, drop grants,
release guests). Guest
containers named `rusui-*` are not in the backup; destroy them with the
container CLI if any are left. New container names carry the database's
plane id (`rusui-<plane id>-<environment name>`), so two planes or a replaced database on
one host do not collide. Restore rotates the plane id and expires every
managed environment, so a restored copy beside its source provisions new
guests on the next turn instead of attaching the source's.

Uninstall: stop the process, delete the database and snapshot directory,
unset the env vars listed in [configuration.md](configuration.md).

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Server exits immediately | Missing GitHub token or webhook/worker/Slack secret; or use `-allow-insecure` locally |
| `GET /healthz` fails | Process not listening; wrong `-addr` |
| `GET /readyz` 503 / `diagnose` exit 1 | Read the JSON checks: missing runtime is `unavailable`; bad policy/TLS/CA is `misconfigured` |
| GitHub webhook 401 | Secret mismatch, or tunnel not hitting `/hooks/github` |
| Runner `auth` / 401 | Secret mismatch between processes |
| Slack 403 `user` | `RUSUI_SLACK_USERS` missing that user id, or empty |
| Slack 401 | Signing secret / timestamp skew > 5 minutes |
| Claim never returns work | Repo not bound in policy, paused, or daily budget exhausted |
| Reconcile skipped | `RUSUI_GITHUB_HOOK_IDS` unset |
| No GitHub comment appeared | Apply is dry-run; look at intended-actions in SQLite |
| Policy change ignored | Slack `reload` or restart |
